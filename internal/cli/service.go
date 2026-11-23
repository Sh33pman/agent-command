package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/version"
)

// The service: the daemon as a per-user systemd unit, launchd agent or
// Windows logon task
// (internal/service). Once it's installed, nothing starts a daemon outside it:
// `acmd daemon` hands off to it, and restarts go through it.

// localService is this machine's service manager; swappable in tests.
var localService = service.Local

// startedByService is true in a daemon the service manager started.
var startedByService bool

// endDaemon ends this process's daemon, when it's one: the init op uses it to
// hand over to the service it installed.
var endDaemon struct {
	sync.Mutex
	fn func()
}

// installedService is the service when it's installed here.
func installedService() (service.Env, bool) {
	svc, err := localService()
	if err != nil || !svc.Installed() {
		return service.Env{}, false
	}
	return svc, true
}

// thisBinary is the stable path of this acmd, as init writes it for hooks.
func thisBinary() string {
	exe, _ := executable()
	return stableBinary(exe)
}

// stableBinary is the path to write for exe: one that stays put across
// upgrades. On Windows it's exe itself: Scoop runs acmd.exe from its current
// folder, which each update points at the new version, and the logon task's
// launcher is next to it there (a PATH shim has none).
func stableBinary(exe string) string {
	if runtime.GOOS == "windows" && exe != "" {
		return exe
	}
	return setup.StableBinary(invokedAs, exe, exec.LookPath)
}

type serviceOptions struct {
	// inDaemon is true for the init op, which runs in the daemon.
	inDaemon bool
}

// installService installs the service for binary and hands over from a daemon
// running outside it. It returns what to tell the user. Where there's no
// service manager, it says why, and the daemon keeps starting on demand.
func installService(ctx context.Context, p paths.Paths, binary string, o serviceOptions) ([]string, error) {
	svc, err := localService()
	if err != nil {
		return nil, err
	}
	ch, err := svc.Install(ctx, binary, service.InstallOptions{Self: o.inDaemon && startedByService})
	if errors.Is(err, service.ErrUnsupported) {
		return []string{unsupportedText(err)}, nil
	}
	if err != nil {
		return ch.Notes, err
	}
	notes := ch.Notes
	switch {
	case o.inDaemon && !startedByService:
		// This daemon answers the op, then makes way for the service's, which
		// is waiting for its lock.
		endDaemon.Lock()
		if end := endDaemon.fn; end != nil {
			time.AfterFunc(time.Second, end)
		}
		endDaemon.Unlock()
	case !o.inDaemon:
		if err := handOver(ctx, p, svc); err != nil {
			return notes, err
		}
	}
	return notes, nil
}

// handOver stops a daemon running outside the service, and starts the
// service's. Sessions keep running, and clients reconnect.
func handOver(ctx context.Context, p paths.Paths, svc service.Env) error {
	if pid := outsidePid(ctx, p, svc); pid > 0 {
		stopDaemon(ctx, p)
	}
	if svc.Running(ctx) {
		return nil
	}
	return svc.Start(ctx)
}

// outsidePid is the pid of a daemon that runs, but not as the service: 0 when
// none does.
func outsidePid(ctx context.Context, p paths.Paths, svc service.Env) int {
	dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := ipc.Dial(dctx, p.Socket(), ipc.HelloFrom(version.Get().Version))
	var mismatch *ipc.ErrVersionMismatch
	if err != nil && !errors.As(err, &mismatch) {
		return 0
	}
	pid := conn.Daemon.Pid
	_ = conn.Close()
	if pid == 0 || pid == svc.Pid(ctx) {
		return 0
	}
	return pid
}

// unsupportedText is why there's no service here, as a sentence.
func unsupportedText(err error) string {
	return sentenceOf(strings.TrimPrefix(err.Error(), service.ErrUnsupported.Error()+": "))
}

func sentenceOf(s string) string {
	if s == "" || strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}

// restartService restarts the service's daemon and waits for it to answer
// as this build. A daemon of another build after the restart means the service
// runs another acmd: that's an error, not a reason to run one outside it.
func restartService(ctx context.Context, p paths.Paths, svc service.Env) (*ipc.Conn, error) {
	if err := svc.Restart(ctx); err != nil {
		return nil, err
	}
	me := ipc.HelloFrom(version.Get().Version)
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := ipc.Dial(ctx, p.Socket(), me)
		if err == nil {
			return conn, nil
		}
		var mismatch *ipc.ErrVersionMismatch
		if errors.As(err, &mismatch) {
			_ = conn.Close()
			// The old daemon may still be on its way out.
			if time.Now().Before(deadline) && ctx.Err() == nil && !restarted(ctx, svc, conn.Daemon.Pid) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return nil, otherBuildError(svc, mismatch.Daemon.Version)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("the service didn't start the daemon (see %s): %w", p.DaemonLog(), err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// restarted says whether the daemon with pid is the service's after the
// restart, so its version is what the service runs.
func restarted(ctx context.Context, svc service.Env, pid int) bool {
	return pid > 0 && pid == svc.Pid(ctx)
}

func otherBuildError(svc service.Env, daemonVersion string) error {
	bin := svc.Status(context.Background()).Binary
	return fmt.Errorf("the service runs %s (acmd %s), and this is acmd %s: run `acmd service install` from this acmd to point the service at it",
		bin, daemonVersion, version.Get().Version)
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Run the daemon as a service that starts at boot or login",
		Long: "acmd's daemon runs as a per-user service, as you: a systemd user unit on Linux, with lingering so it\n" +
			"starts at boot, a launchd agent on macOS, which starts at login, and a logon task in Task Scheduler on\n" +
			"Windows, which starts it hidden at logon. `acmd init` installs it.\n" +
			"Sessions keep running when the service restarts.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newServiceInstallCmd(), newServiceUninstallCmd(), newServiceStatusCmd())
	return cmd
}

func newServiceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install and start the service (safe to rerun)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			if err := p.Ensure(); err != nil {
				return err
			}
			svc, err := localService()
			if err != nil {
				return err
			}
			if _, err := svc.Manager(); err != nil {
				return errors.New(unsupportedText(err))
			}
			notes, err := installService(cmd.Context(), p, thisBinary(), serviceOptions{})
			for _, n := range notes {
				fmt.Fprintln(cmd.ErrOrStderr(), n)
			}
			if err != nil {
				return err
			}
			return printServiceStatus(cmd, svc)
		},
	}
}

func newServiceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the service (the daemon then starts on demand)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := localService()
			if err != nil {
				return err
			}
			if _, err := svc.Manager(); err != nil {
				return errors.New(unsupportedText(err))
			}
			removed, err := svc.Uninstall(cmd.Context())
			if err != nil {
				return err
			}
			if !jsonOut {
				if removed {
					fmt.Fprintf(cmd.OutOrStdout(), "Removed the service (%s). Sessions keep running, and the daemon starts when something needs it.\n", tildeHome(svc.File()))
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "The service wasn't installed.")
				}
				return nil
			}
			return printJSON(jsonWriter(cmd), svc.Status(cmd.Context()))
		},
	}
}

func newServiceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Say whether the service is installed and running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := localService()
			if err != nil {
				return err
			}
			return printServiceStatus(cmd, svc)
		},
	}
}

func printServiceStatus(cmd *cobra.Command, svc service.Env) error {
	s := svc.Status(cmd.Context())
	if jsonOut {
		return printJSON(jsonWriter(cmd), s)
	}
	writeServiceStatus(cmd.OutOrStdout(), s)
	return nil
}

func writeServiceStatus(w io.Writer, s service.Status) {
	switch {
	case !s.Supported:
		fmt.Fprintln(w, sentenceOf(s.Note))
		return
	case !s.Installed:
		fmt.Fprintf(w, "The service isn't installed (%s): the daemon starts when something needs it. Install it with `acmd service install`.\n", s.Manager)
		return
	}
	state := "not running: start it with `acmd daemon restart`"
	if s.Running {
		state = fmt.Sprintf("running (pid %d)", s.Pid)
	}
	fmt.Fprintf(w, "The service is installed and %s.\n", state)
	fmt.Fprintf(w, "  %-8s %s (%s)\n", "file", tildeHome(s.File), s.Manager)
	fmt.Fprintf(w, "  %-8s %s\n", "runs", tildeHome(s.Binary))
	switch {
	case s.Linger == nil:
		fmt.Fprintf(w, "  %-8s %s\n", "starts", "at login")
	case *s.Linger:
		fmt.Fprintf(w, "  %-8s %s\n", "starts", "at boot (lingering is on)")
	default:
		fmt.Fprintf(w, "  %-8s %s\n", "starts", "at login: lingering is off, so turn it on with `sudo loginctl enable-linger "+userName()+"` to start it at boot")
	}
}

func tildeHome(path string) string {
	home, _ := os.UserHomeDir()
	return setup.Tilde(path, home)
}

func userName() string {
	if svc, err := localService(); err == nil && svc.User != "" {
		return svc.User
	}
	return "$USER"
}
