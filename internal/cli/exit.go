package cli

import (
	"errors"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// ExitError is an error with an exit code of its own, so scripts can tell
// failures apart (see acmd --help).
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode is the code acmd exits with after err: 0 for none, 1 unless err
// says otherwise.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	var ue usageError
	if errors.As(err, &ue) {
		return exitUsage
	}
	var re *ipc.RemoteError
	if errors.As(err, &re) {
		if code, ok := codedExits[re.Code]; ok {
			return code
		}
	}
	return exitFailed
}
