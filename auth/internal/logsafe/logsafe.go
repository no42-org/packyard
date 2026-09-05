/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Package logsafe neutralises untrusted strings before they reach the logger.
//
// The auth service logs with slog's JSON handler, which already escapes
// control characters, so a forged line break cannot split a log record.
// Sanitising at the call site keeps that guarantee independent of the
// handler in use and makes the intent visible where request-controlled
// values (path segments, IDs, provider names) are logged.
package logsafe

import (
	"log/slog"
	"strings"
)

// String returns s with carriage returns and line feeds replaced by their
// escaped spellings, so a value cannot inject a new log line.
func String(s string) string {
	s = strings.ReplaceAll(s, "\r", `\r`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

// Attr is slog.String with the value passed through String.
func Attr(key, value string) slog.Attr {
	return slog.String(key, String(value))
}
