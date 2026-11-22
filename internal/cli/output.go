package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// --json: every command that reads or changes state prints the answer of the
// op it ran, in the daemon's wire types, and nothing else on stdout. Errors go
// to stderr as {"error": {"code", "message"}}, and the exit code says what
// kind (exitCodesHelp).

var (
	// jsonOut is --json.
	jsonOut bool
	// jsonStdout is where JSON goes while --json hides the commands' text.
	jsonStdout = io.Discard
	// answers are the op answers a command got, to print with --json when it
	// printed none itself; emitted says it did.
	answers [][]byte
	emitted bool
)

// startOutput readies --json for a command: its text goes nowhere.
func startOutput(cmd *cobra.Command) {
	answers, emitted = nil, false
	if !jsonOut {
		return
	}
	jsonStdout = cmd.OutOrStdout()
	cmd.SetOut(io.Discard)
}

// jsonWriter is where a command writes its JSON.
func jsonWriter(cmd *cobra.Command) io.Writer {
	if !jsonOut {
		return cmd.OutOrStdout()
	}
	emitted = true
	return jsonStdout
}

// record keeps an op's answer for --json. The snapshot a command reads to find
// what it acts on isn't its answer: a command whose answer it is prints it
// itself.
func record(op string, raw json.RawMessage) {
	if !jsonOut || op == ipc.OpSnapshot {
		return
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	answers = append(answers, raw)
}

// finishOutput prints, with --json, the answers of the ops a command ran when
// it printed nothing itself: one answer as it is, several one per line.
func finishOutput() error {
	if !jsonOut || emitted {
		return nil
	}
	for _, a := range answers {
		if _, err := fmt.Fprintln(jsonStdout, string(a)); err != nil {
			return err
		}
	}
	return nil
}

// Exit codes. 3 to 8 are a Remote host that can't be used.
const (
	exitFailed       = 1  // anything else
	exitUsage        = 2  // the command line doesn't make sense
	exitNotFound     = 9  // no such session, group, job or host
	exitInvalid      = 10 // the request doesn't make sense (a bad name, value or schedule)
	exitNotSupported = 11 // the host can't do that, or a paired device may not
	exitNotRunning   = 12 // the session's terminal has ended
	exitPartial      = 13 // some of several ids didn't change
	exitConflict     = 14 // it changed since it was loaded
)

// exitCodesHelp lists the exit codes, for acmd --help.
const exitCodesHelp = "Exit codes:\n" +
	"  0   done\n" +
	"  1   failed, for any reason not below\n" +
	"  2   usage: the command line doesn't make sense\n" +
	"  3   a Remote host is unreachable: no answer from its address, or the connection dropped\n" +
	"  4   a Remote host is waiting for its user to approve this machine\n" +
	"  5   a Remote host refused this machine\n" +
	"  6   a Remote host's fingerprint isn't the one you pinned\n" +
	"  7   a Remote host speaks another protocol version\n" +
	"  8   Hosting is on, but can't listen on its port\n" +
	"  9   not found: no such session, group, job or host\n" +
	"  10  invalid: a bad name, value or schedule\n" +
	"  11  not supported: the host can't do that, or this client may not\n" +
	"  12  not running: the session's terminal has ended\n" +
	"  13  partial: some of several sessions didn't change\n" +
	"  14  conflict: it changed while acmd was saving it: run the command again\n" +
	"With --json, an error is {\"error\": {\"code\": …, \"message\": …}} on stderr."

// codedExits are the daemon's error codes and their exit codes.
var codedExits = map[string]int{
	ipc.CodeNotFound: exitNotFound, ipc.CodeInvalid: exitInvalid, ipc.CodeBadRequest: exitInvalid,
	ipc.CodeNotSupported: exitNotSupported, errcode.Forbidden: exitNotSupported, ipc.CodeUnknownOp: exitNotSupported,
	ipc.CodePermission: exitNotSupported, ipc.CodeConflict: exitConflict,
	ipc.CodeNotRunning: exitNotRunning, ipc.CodeHostUnreachable: exitUnreachable,
}

// exitNames are the codes JSON errors carry for acmd's own exit codes.
var exitNames = map[int]string{
	exitFailed: ipc.CodeInternal, exitUsage: "usage", exitUnreachable: ipc.CodeHostUnreachable, exitPending: "pending",
	exitUnauthorized: "unauthorized", exitFingerprint: "fingerprint", exitVersion: "version", exitPortTaken: "port_taken",
	exitNotFound: ipc.CodeNotFound, exitInvalid: ipc.CodeInvalid, exitNotSupported: ipc.CodeNotSupported,
	exitNotRunning: ipc.CodeNotRunning, exitPartial: "partial", exitConflict: ipc.CodeConflict,
}

// usageError is a command line that doesn't make sense.
type usageError struct{ error }

func (e usageError) Unwrap() error { return e.error }

// errorCode is err's code: the daemon's, or the one for acmd's exit code.
func errorCode(err error) string {
	var re *ipc.RemoteError
	if errors.As(err, &re) && re.Code != "" {
		return re.Code
	}
	var coded interface{ Code() string }
	if errors.As(err, &coded) && coded.Code() != "" {
		return coded.Code()
	}
	return exitNames[ExitCode(err)]
}

// WriteError writes err for a person, or with --json as JSON.
func WriteError(w io.Writer, err error) {
	if !jsonOut {
		fmt.Fprintln(w, "error:", err)
		return
	}
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": errorCode(err), "message": err.Error()}})
	fmt.Fprintln(w, string(b))
}

// emitJSON prints v with --json, for a command whose answer is part of the
// snapshot (list, group list, schedule list), and says whether it did.
func emitJSON(cmd *cobra.Command, v any) (bool, error) {
	if !jsonOut {
		return false, nil
	}
	enc := json.NewEncoder(jsonWriter(cmd))
	enc.SetIndent("", "  ")
	return true, enc.Encode(v)
}
