#!/bin/sh
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# init-rpm-tree.sh — prepare the RPM tree root on container start.
#
# Runs as the nginx user (uid 101) from /docker-entrypoint.d. The tree is
# shared with two other writers that run as root with every capability
# dropped: the auth service, which creates component directories from the
# component record, and the promotion exec that publishes packages. Root
# without CAP_DAC_OVERRIDE is bound by ordinary permissions, so the tree is
# owned by group 101, setgid so new entries inherit the group, and
# group-writable. auth joins group 101 via group_add in compose.yml; the
# promotion exec runs as 0:101.
#
# Component and series directories are NOT created here. The component
# record owns them (POST /api/v1/components).
set -e
ROOT="/usr/share/nginx/html"
mkdir -p "${ROOT}/rpm"
chmod 2775 "${ROOT}/rpm"
# Normalise directories this user owns from earlier image versions, which
# created them 0755 and therefore unwritable for the other writers.
find "${ROOT}/rpm" -type d -user "$(id -u)" ! -perm 2775 -exec chmod 2775 {} + 2>/dev/null || true
echo "RPM tree root ready at ${ROOT}/rpm/ (group $(id -g), setgid, group-writable)"
