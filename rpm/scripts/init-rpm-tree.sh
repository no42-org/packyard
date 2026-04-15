#!/bin/sh
# init-rpm-tree.sh — creates the series-versioned, per-OS RPM directory tree on first start
# Runs via /docker-entrypoint.d/ before nginx starts (nginx:alpine entrypoint pattern)
# Idempotent: mkdir -p is safe to run on every container start

set -e

COMPONENTS="core minion sentinel"
SERIES="2025"
OS_TARGETS="el8-x86_64 el9-x86_64 el10-x86_64 centos10-x86_64"
ROOT="/usr/share/nginx/html"

for component in $COMPONENTS; do
    for series in $SERIES; do
        for os in $OS_TARGETS; do
            mkdir -p "${ROOT}/rpm/${component}/${series}/${os}"
        done
    done
done

echo "RPM directory tree initialised under ${ROOT}/rpm/"
