// Command fakeclaude stands in for `claude` in tests (SPEC §12 Testing).
// It keeps its sessions in $FAKECLAUDE_STATE (a JSON file) and emulates:
//
//	claude --bg [--name N] [args…] [-- prompt]   prints "backgrounded · <id> · …"
//	claude --bg --resume <session id>           same, keeping the id (M0 C3)
//	claude attach <id>                          prints "ATTACHED <id>", runs until SIGTERM
//	                                            or a "detach" line on stdin (exit 0);
//	                                            unknown id: exits 1 at once
//	claude agents --json [--all]
//	claude stop <id>
//	claude                                      (no args: the trust prompt) waits for
//	                                            SIGTERM or a line on stdin
//
// A folder containing a file named ".untrusted" is not trusted until a
// plain `claude` ran there and got an "accept" line on stdin.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

type agent struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status"`
	Pid       *int   `json:"pid"`
}

type state struct {
	Agents  []agent  `json:"agents"`
	Trusted []string `json:"trusted"`
	Calls   []string `json:"calls"`
}

func main() {
	path := os.Getenv("FAKECLAUDE_STATE")
	args := os.Args[1:]
	st := update(path, func(st *state) { st.Calls = append(st.Calls, strings.Join(args, " ")) })
	cwd, _ := os.Getwd()

	switch {
	case len(args) == 0:
		trustPrompt(path, cwd)
	case args[0] == "--bg":
		if !trusted(st, cwd) {
			fmt.Println("Error: Workspace not trusted. Run `claude` in this folder once to accept.")
			os.Exit(1)
		}
		sid := newID()
		name := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--resume":
				if i+1 < len(args) {
					sid = args[i+1]
				}
			case "--name":
				if i+1 < len(args) {
					name = args[i+1]
				}
			}
		}
		update(path, func(st *state) {
			pid := 4000 + len(st.Agents)
			replaced := false
			for i, a := range st.Agents {
				if a.SessionID == sid {
					st.Agents[i].Pid, st.Agents[i].Status, replaced = &pid, "idle", true
				}
			}
			if !replaced {
				st.Agents = append(st.Agents, agent{ID: sid[:8], SessionID: sid, Kind: "background", Cwd: cwd, Name: name, Status: "idle", Pid: &pid})
			}
		})
		fmt.Printf("backgrounded · %s · claude attach %s\n", sid[:8], sid[:8])
	case args[0] == "attach" && len(args) == 2:
		found := false
		for _, a := range st.Agents {
			if a.ID == args[1] {
				found = true
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "No session %s\n", args[1])
			os.Exit(1)
		}
		fmt.Printf("ATTACHED %s\n", args[1])
		if typed := os.Getenv("FAKECLAUDE_TYPED"); typed != "" {
			// Record what's typed; Ctrl+Z (SIGTSTP in a terminal) detaches,
			// like the real attach.
			tstp := make(chan os.Signal, 1)
			signal.Notify(tstp, syscall.SIGTSTP)
			go func() {
				sc := bufio.NewScanner(os.Stdin)
				for sc.Scan() {
					f, err := os.OpenFile(filepath.Clean(typed), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 G703 -- the test's own file
					if err == nil {
						fmt.Fprintln(f, sc.Text())
						_ = f.Close()
					}
				}
			}()
			<-tstp
			return
		}
		waitForEnd("detach", false)
	case args[0] == "agents":
		_ = json.NewEncoder(os.Stdout).Encode(st.Agents)
	case args[0] == "stop" && len(args) == 2:
		update(path, func(st *state) {
			for i, a := range st.Agents {
				if a.ID == args[1] {
					st.Agents[i].Pid = nil
				}
			}
		})
	default:
		fmt.Fprintf(os.Stderr, "fakeclaude: unsupported %q\n", args)
		os.Exit(2)
	}
}

// trustPrompt plays the interactive trust dialog: "accept" trusts cwd.
func trustPrompt(path, cwd string) {
	fmt.Println("TRUST PROMPT for " + cwd)
	if waitForEnd("accept", true) {
		update(path, func(st *state) { st.Trusted = append(st.Trusted, cwd) })
		if f := os.Getenv("FAKECLAUDE_TRUST_FILE"); f != "" {
			b, _ := json.Marshal(map[string]any{"projects": map[string]any{cwd: map[string]bool{"hasTrustDialogAccepted": true}}})
			_ = os.WriteFile(f, b, 0o600) // #nosec G703 -- test helper
		}
		fmt.Println("TRUSTED")
		waitForEnd("", false) // like the real prompt: stays open until closed
	}
}

// waitForEnd blocks until SIGTERM/SIGHUP (false) or a line equal to word
// on stdin (true; "" matches any line). EOF ends it (false) if eofEnds,
// else it keeps waiting for a signal.
func waitForEnd(word string, eofEnds bool) bool {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP)
	lines := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if word == "" || strings.TrimSpace(sc.Text()) == word {
				lines <- true
				return
			}
		}
		if eofEnds {
			lines <- false
			return
		}
		select {} // EOF: keep waiting for a signal
	}()
	select {
	case <-sig:
		return false
	case ok := <-lines:
		return ok
	}
}

func trusted(st state, cwd string) bool {
	if _, err := os.Stat(filepath.Join(cwd, ".untrusted")); err != nil {
		return true
	}
	for _, t := range st.Trusted {
		if t == cwd {
			return true
		}
	}
	return false
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// update changes the state file under an exclusive lock (several fake
// claude processes run at once: the daemon polls `agents` constantly).
func update(path string, fn func(*state)) state {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 G703 -- test helper
	if err == nil {
		defer lock.Close()
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	}
	st := load(path)
	fn(&st)
	save(path, st)
	return st
}

func load(path string) state {
	var st state
	if b, err := os.ReadFile(path); err == nil { // #nosec G304 G703 -- test helper
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// save writes atomically, so readers never see a partial file.
func save(path string, st state) {
	b, _ := json.MarshalIndent(st, "", "  ")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil { // #nosec G703 -- test helper
		_ = os.Rename(tmp, path) // #nosec G703 -- test helper
	}
}
