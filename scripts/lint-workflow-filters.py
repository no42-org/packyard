#!/usr/bin/env python3
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
"""Validate embedded YAML that workflow linters cannot see.

dorny/paths-filter takes its `filters` input as a YAML *string*, so a bad
indentation inside it passes actionlint and zizmor and only fails at run
time, skipping every gate. This script parses each workflow, finds
paths-filter steps and checks that `filters` is valid YAML mapping names
to lists of glob strings.
"""
import glob
import sys

import yaml


def check(path: str) -> list[str]:
    errors: list[str] = []
    with open(path, encoding="utf-8") as fh:
        doc = yaml.safe_load(fh)
    for job_name, job in (doc.get("jobs") or {}).items():
        for idx, step in enumerate(job.get("steps") or []):
            uses = str(step.get("uses", ""))
            if not uses.startswith("dorny/paths-filter@"):
                continue
            where = f"{path}: job {job_name!r} step {idx + 1}"
            raw = (step.get("with") or {}).get("filters")
            if not isinstance(raw, str):
                errors.append(f"{where}: filters must be a YAML string")
                continue
            try:
                parsed = yaml.safe_load(raw)
            except yaml.YAMLError as exc:
                errors.append(f"{where}: filters is not valid YAML: {exc}")
                continue
            if not isinstance(parsed, dict) or not parsed:
                errors.append(f"{where}: filters must map names to glob lists")
                continue
            for name, globs in parsed.items():
                if not isinstance(globs, list) or not all(isinstance(g, str) for g in globs):
                    errors.append(f"{where}: filter {name!r} must be a list of glob strings")
    return errors


def main() -> int:
    files = sorted(glob.glob(".github/workflows/*.yml"))
    errors = [e for f in files for e in check(f)]
    for e in errors:
        print(f"error: {e}", file=sys.stderr)
    print(f"checked {len(files)} workflow files, {len(errors)} error(s)")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
