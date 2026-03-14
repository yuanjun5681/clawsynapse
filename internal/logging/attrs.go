package logging

import (
	"log/slog"
	"unicode/utf8"
)

func Event(v string) slog.Attr {
	return slog.String("event", v)
}

func Peer(v string) slog.Attr {
	return slog.String("peer", v)
}

func Subject(v string) slog.Attr {
	return slog.String("subject", v)
}

func MessageID(v string) slog.Attr {
	return slog.String("messageId", v)
}

func MessageType(v string) slog.Attr {
	return slog.String("messageType", v)
}

func RequestID(v string) slog.Attr {
	return slog.String("requestId", v)
}

func CorrelationID(v string) slog.Attr {
	return slog.String("correlationId", v)
}

func From(v string) slog.Attr {
	return slog.String("from", v)
}

func To(v string) slog.Attr {
	return slog.String("to", v)
}

func TrustStatus(v string) slog.Attr {
	return slog.String("trustStatus", v)
}

func AuthStatus(v string) slog.Attr {
	return slog.String("authStatus", v)
}

func Error(err error) slog.Attr {
	if err == nil {
		return slog.String("error", "")
	}
	return slog.String("error", err.Error())
}

func SessionKey(v string) slog.Attr {
	return slog.String("sessionKey", v)
}

func ContentPreview(v string, maxRunes int) slog.Attr {
	if maxRunes <= 0 {
		maxRunes = 160
	}
	if utf8.RuneCountInString(v) <= maxRunes {
		return slog.String("contentPreview", v)
	}

	runes := []rune(v)
	return slog.String("contentPreview", string(runes[:maxRunes])+"...")
}

func ContentLength(v string) slog.Attr {
	return slog.Int("contentLength", utf8.RuneCountInString(v))
}
