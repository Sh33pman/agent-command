package cli

import "errors"

// ExitError is an error with an exit code of its own, so scripts can tell
// failures apart (see acmd remote --help).
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
	return 1
}
