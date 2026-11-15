package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Exit codes. 0..4 are the meanings this tool can distinguish; there is no cost
// or budget stop (the tool does not price a call), so 5 is not used. Every failure
// also carries a machine readable Reason.
const (
	ExitOK      = 0
	ExitGeneral = 1 // validation failed, partial failure, evidence problem
	ExitConfig  = 2 // configuration / usage
	ExitAuth    = 3 // credential missing (nothing was sent)
	ExitAPI     = 4 // API or transport failure
)

// ExitError carries both the process exit code and a stable reason string.
type ExitError struct {
	Code    int
	Reason  string
	Message string
}

func (e *ExitError) Error() string { return e.Message }

// failf builds an ExitError.
func failf(code int, reason string, format string, args ...any) error {
	return &ExitError{Code: code, Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// asExit extracts an ExitError, or wraps an arbitrary error as a general failure.
func asExit(err error) *ExitError {
	var target *ExitError
	if errors.As(err, &target) {
		return target
	}
	return &ExitError{Code: ExitGeneral, Reason: "internal", Message: err.Error()}
}

// writeError prints the failure in the requested format.
func writeError(w io.Writer, err *ExitError, asJSON bool) {
	if asJSON {
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(map[string]any{
			"error": map[string]any{
				"code":    err.Code,
				"reason":  err.Reason,
				"message": err.Message,
			},
		})
		return
	}
	fmt.Fprintf(w, "error: %s\n", err.Message)
}
