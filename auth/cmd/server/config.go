/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package main

import (
	"fmt"
	"os"
	"time"
)

// envDuration reads a Go duration from the environment variable key.
//
// Unset or empty returns def. The value must parse with time.ParseDuration,
// so a bare number such as "30" is rejected: operators must write "30s".
// Negative values and values above max are rejected. Zero is allowed so a
// caller can use it to mean "disabled".
func envDuration(key string, def, max time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a valid duration; use Go duration syntax such as \"30s\" or \"1m\"", key, raw)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s=%q must not be negative", key, raw)
	}
	if d > max {
		return 0, fmt.Errorf("%s=%q exceeds the maximum of %s", key, raw, max)
	}
	return d, nil
}
