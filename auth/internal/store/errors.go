/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package store

import (
	"fmt"
	"strings"
)

// classifyConstraintError inspects a SQLite constraint error and maps it to a
// typed sentinel from this package. modernc.org/sqlite reports constraints
// via prefixed substrings in the error message; we differentiate UNIQUE
// (duplicate), FOREIGN KEY (missing parent), and CHECK (invalid input) so
// handlers can return the right HTTP status. The mapping arg controls which
// sentinel each constraint type maps to — account vs operator paths use
// different invalid-input sentinels.
//
// Anything that doesn't match a known constraint is surfaced wrapped so
// callers see the underlying driver message.
type constraintErrorMap struct {
	UniqueErr  error
	ForeignErr error
	CheckErr   error
}

func classify(op string, err error, m constraintErrorMap) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed") && m.UniqueErr != nil:
		return fmt.Errorf("%s: %w", op, m.UniqueErr)
	case strings.Contains(msg, "FOREIGN KEY constraint failed") && m.ForeignErr != nil:
		return fmt.Errorf("%s: %w", op, m.ForeignErr)
	case strings.Contains(msg, "CHECK constraint failed") && m.CheckErr != nil:
		return fmt.Errorf("%s: %w", op, m.CheckErr)
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}

// Account-path classifier preserved for the existing accounts.go callers.
func classifyConstraintError(op string, err error) error {
	return classify(op, err, constraintErrorMap{
		UniqueErr:  ErrAccountEmailExists,
		ForeignErr: ErrAccountNotFound,
		CheckErr:   ErrAccountInvalid,
	})
}
