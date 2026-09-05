/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package logsafe

import "testing"

func TestString(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		"line\nbreak":      `line\nbreak`,
		"cr\rlf\n":         `cr\rlf\n`,
		"":                 "",
		"unicode ✓ intact": "unicode ✓ intact",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAttr(t *testing.T) {
	a := Attr("k", "a\nb")
	if a.Key != "k" || a.Value.String() != `a\nb` {
		t.Errorf("Attr = %v", a)
	}
}
