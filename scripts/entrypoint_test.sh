#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
for command in mkdir chown chmod; do
    printf '#!/bin/sh\nexit 0\n' > "$work/bin/$command"
done
cat > "$work/bin/su-exec" <<'SH'
#!/bin/sh
printf '%s\n' "$@"
SH
chmod +x "$work/bin/"*
actual="$(PATH="$work/bin:$PATH" sh docker/panel-entrypoint.sh)"
expected=$'rapido:rapido\n/app/panel'
[ "$actual" = "$expected" ] || { echo 'FAIL: entrypoint did not supply the default panel command'; exit 1; }
actual="$(PATH="$work/bin:$PATH" sh docker/panel-entrypoint.sh /app/panel version)"
expected=$'rapido:rapido\n/app/panel\nversion'
[ "$actual" = "$expected" ] || { echo 'FAIL: entrypoint did not preserve an explicit command'; exit 1; }
echo 'Entrypoint default command and argument forwarding passed.'
