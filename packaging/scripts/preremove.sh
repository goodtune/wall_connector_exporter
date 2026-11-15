#!/bin/sh
set -e

# Keep the service on an upgrade: deb gives "upgrade" and rpm gives 1.
case "$1" in
    upgrade | 1) exit 0 ;;
esac

if command -v systemctl >/dev/null 2>&1; then
    systemctl stop wall_connector_exporter.service 2>/dev/null || true
    systemctl disable wall_connector_exporter.service 2>/dev/null || true
fi
