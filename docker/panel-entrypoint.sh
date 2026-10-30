#!/bin/sh
set -eu

# The panel needs write access only to its backup directory and the shared
# SSH known_hosts volume. Preserve existing data while repairing ownership
# when upgrading from the previous root-running image.
mkdir -p /app/db_backups /var/lib/rapido-go/ssh
chown -R rapido:rapido /app/db_backups /var/lib/rapido-go/ssh
# A backup holds every credential the panel has.
chmod -R go-rwx /app/db_backups

if [ "$#" -eq 0 ]; then
    set -- /app/panel
fi

exec su-exec rapido:rapido "$@"
