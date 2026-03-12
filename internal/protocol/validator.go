package protocol

import (
	"strings"
	"time"
)

var supportedModules = map[string]struct{}{
	"auth":      {},
	"trust":     {},
	"discovery": {},
	"control":   {},
	"msg":       {},
	"events":    {},
	"pubsub":    {},
	"transfer":  {},
}

type ValidateOptions struct {
	Now           time.Time
	MaxMessageAge time.Duration
	MaxFutureSkew time.Duration
}

func ValidateMessage(subject string, msg ControlMessage, opts ValidateOptions) error {
	if err := ValidateSubject(subject); err != nil {
		return err
	}

	if strings.TrimSpace(msg.MessageType) == "" {
		return NewError(ErrMissingMessageType, "messageType is required")
	}

	subMod, err := SubjectModule(subject)
	if err != nil {
		return err
	}
	msgMod := messageTypeModule(msg.MessageType)
	if _, ok := supportedModules[msgMod]; !ok {
		return NewError(ErrUnsupportedModuleType, "messageType module is unsupported")
	}
	if subMod != msgMod {
		return NewError(ErrModuleMismatch, "subject module and messageType module mismatch")
	}

	subTarget, err := SubjectTarget(subject)
	if err != nil {
		return err
	}
	if msg.To != "" && subTarget != "global" && subTarget != msg.To {
		return NewError(ErrTargetMismatch, "subject target and payload.to mismatch")
	}

	if msg.Ts > 0 {
		now := opts.Now
		if now.IsZero() {
			now = time.Now()
		}
		maxAge := opts.MaxMessageAge
		if maxAge == 0 {
			maxAge = 5 * time.Minute
		}
		maxSkew := opts.MaxFutureSkew
		if maxSkew == 0 {
			maxSkew = 30 * time.Second
		}

		ts := time.UnixMilli(msg.Ts)
		if now.Sub(ts) > maxAge || ts.Sub(now) > maxSkew {
			return NewError(ErrInvalidTimestamp, "message timestamp exceeds accepted window")
		}
	}

	return nil
}

func messageTypeModule(t string) string {
	parts := strings.Split(strings.TrimSpace(t), ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
