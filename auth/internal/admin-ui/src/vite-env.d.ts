/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Pulls in Vite's ambient module declarations (*.css, *.svg, import.meta.env)
// so side-effect imports like `import "./styles.css"` type-check. TypeScript 7
// enforces noUncheckedSideEffectImports by default, which made this mandatory.
/// <reference types="vite/client" />
