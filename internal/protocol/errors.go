package protocol

import "fmt"

const (
	ErrInvalidSubject        = "protocol.invalid_subject"
	ErrTargetMismatch        = "protocol.subject_target_mismatch"
	ErrModuleMismatch        = "protocol.module_mismatch"
	ErrMissingMessageType    = "protocol.missing_message_type"
	ErrUnsupportedSubject    = "protocol.unsupported_subject"
	ErrMissingTarget         = "protocol.missing_target"
	ErrInvalidTimestamp      = "auth.clock_skew"
	ErrReplayDetected        = "auth.replay_detected"
	ErrUnsupportedModuleType = "protocol.unsupported_message_type"
	ErrTrustAlreadyPending   = "trust.already_pending"
	ErrTrustAlreadyTrusted   = "trust.already_trusted"
	ErrTrustNotFound         = "trust.not_found"
	ErrTrustRejected         = "trust.rejected"
	ErrTrustRevoked          = "trust.revoked"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code, message string) error {
	return &Error{Code: code, Message: message}
}
