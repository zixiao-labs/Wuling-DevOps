// Package applog wires up the global structured logger.
//
// The package name is "applog" rather than "log" to avoid shadowing
// the stdlib package and the Go convention against doing so.
package applog

import (
	"log/slog"
	"os"
	"strings"
)

// New constructs a slog.Logger from textual level/format settings.
// Unknown levels fall back to info; unknown formats fall back to text.
func New(level, format string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lv, AddSource: lv == slog.LevelDebug}
	var h slog.Handler
	if strings.EqualFold(format, "json") {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
