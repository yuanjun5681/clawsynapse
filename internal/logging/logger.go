package logging

import (
	"io"
	"log/slog"
	"os"
)

type Options struct {
	Level     string
	Format    string
	AddSource bool
	Output    io.Writer
}

func New(opts Options) *slog.Logger {
	var lv slog.Level
	switch opts.Level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}

	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	handlerOpts := &slog.HandlerOptions{
		Level:     lv,
		AddSource: opts.AddSource,
	}

	var h slog.Handler
	if opts.Format == "text" {
		h = slog.NewTextHandler(out, handlerOpts)
	} else {
		h = slog.NewJSONHandler(out, handlerOpts)
	}
	return slog.New(h)
}
