// Package apperror contains stable errors shared by the service and HTTP
// boundaries.
package apperror

import "errors"

const (
	CodeUnauthorized      = "unauthorized"
	CodeWrongKey          = "delta_wrong_key"
	CodeInvalidDate       = "invalid_date"
	CodeEntryNotFound     = "entry_not_found"
	CodeInvalidEntry      = "invalid_entry"
	CodeHabitNotFound     = "habit_not_found"
	CodeHabitNotActive    = "habit_not_active"
	CodeInvalidHabit      = "invalid_habit"
	CodeInvalidGrid       = "invalid_grid"
	CodeInvalidStats      = "invalid_stats"
	CodeNotFound          = "not_found"
	CodeUpgrade           = "upgrade_required"
	CodeInternalError     = "internal_error"
	CodeMethodNotAllowed  = "method_not_allowed"
	CodeSetupKeyShown     = "setup_key_already_shown"
	CodeSetupRequired     = "setup_key_required"
	CodeDatabaseExists    = "database_exists"
	CodeDatabaseNotFound  = "database_not_found"
	CodeInvalidSetup      = "invalid_setup"
	CodeServerUnavailable = "server_unavailable"

	WrongKeyMessage = "wrong key or not a DELTA file — check the key in your password manager"
)

// Error is an error with a stable machine-readable code and user-facing
// message. The wrapped cause is retained for logs and diagnostics.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func New(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Wrap(code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

func Code(err error) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return CodeInternalError
}

func Message(err error) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Message
	}
	return "internal server error"
}
