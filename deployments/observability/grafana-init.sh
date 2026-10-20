#!/bin/sh
# Trusted fixed init only: private host0600 secret is not readable by UID472.
set -eu
umask 0077
bytes=$(wc -c < /run/secrets/grafana_password)
if [ "$bytes" -lt 32 ] || [ "$bytes" -gt 256 ]; then
  echo 'O2 Grafana secret rejected' >&2
  exit 1
fi
mkdir -p /run/o2
cp /run/secrets/grafana_password /run/o2/password
chmod 0700 /run/o2
chmod 0400 /run/o2/password
chown 472:0 /run/o2 /run/o2/password
# Grafana service always runs as the pinned image's actual grafana user.
exec su -s /bin/sh grafana -c 'exec /run.sh'
