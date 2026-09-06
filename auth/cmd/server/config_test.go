/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package main

import (
	"testing"
	"time"
)

// These tests mutate process environment via t.Setenv and must not run in parallel.
func TestEnvDuration(t *testing.T) {
	const key = "PACKYARD_TEST_DURATION"
	def, max := 30*time.Second, time.Hour

	cases := []struct {
		name    string
		set     bool
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "unset returns default", set: false, want: def},
		{name: "valid duration", set: true, value: "45s", want: 45 * time.Second},
		{name: "zero allowed", set: true, value: "0", want: 0},
		{name: "zero with unit allowed", set: true, value: "0s", want: 0},
		{name: "bare number rejected", set: true, value: "30", wantErr: true},
		{name: "garbage rejected", set: true, value: "soon", wantErr: true},
		{name: "negative rejected", set: true, value: "-1s", wantErr: true},
		{name: "above max rejected", set: true, value: "2h", wantErr: true},
		{name: "exactly max allowed", set: true, value: "1h", want: time.Hour},
		{name: "empty string treated as unset", set: true, value: "", want: def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.value)
			}
			got, err := envDuration(key, def, max)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("envDuration(%q) = %v, want error", tc.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("envDuration(%q) unexpected error: %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("envDuration(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
