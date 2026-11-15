#!/bin/sh
set -e

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
    # The unit ships disabled: the exporter cannot start without a charger.
    # Put the chargers in /etc/wall_connector_exporter/config.yml, then:
    #   systemctl enable --now wall_connector_exporter.service
    #
    # On an upgrade, start the new version if the service is running.
    systemctl try-restart wall_connector_exporter.service 2>/dev/null || true
fi
