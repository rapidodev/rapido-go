#!/usr/bin/env bash
#
# Guards the failure mode that silently broke every token-less install: a
# helper whose last command legitimately returns non-zero (a test that is
# false, or a `grep` that simply finds nothing under `pipefail`) is called
# as a plain statement, so `set -euo pipefail` exits the whole installer
# mid-run with status 1 and NO message at all.
#
# Both cases below were real, and the second survived the first fix because
# it only triggers once .env EXISTS but carries no token line - which is
# exactly the state the installer itself creates a few steps earlier.
# Invisible while the repo was private: everyone set RAPIDO_REPO_TOKEN and
# took the early return-0 path.
#
# The second half exercises the panel installer's pure helpers (flag parsing,
# domain handling, sizing, Caddyfile/.env rendering, preflight arithmetic, the
# parallel pull and lightweight fetch logic). None of it needs root, Docker or
# a network: the script is sourced with its final `main "$@"` removed, and
# anything that would touch the outside world is stubbed per case.
set -uo pipefail
cd "$(dirname "$0")/.."

fail=0
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
# Whatever the cases below make for themselves goes with it.
export TMPDIR="$WORK"

# A copy of the script without its final `main "$@"`, safe to source.
prepare() {
    local out="$WORK/$(basename "$1")"
    sed '/^main "\$@"$/d' "$1" > "$out"
    printf '%s' "$out"
}

run_case() {
    local script="$1" label="$2" envsetup="$3" src out
    src="$(prepare "$script")"
    out=$(bash -c '
        set -euo pipefail
        RAPIDO_REPO_TOKEN=""
        APP_DIR=$(mktemp -d)
        '"$envsetup"'
        . "'"$src"'" >/dev/null 2>&1
        load_saved_token
        save_token
        load_saved_token
        echo REACHED_END
        rm -rf "$APP_DIR"
    ' 2>&1) || true
    if [ "$out" = "REACHED_END" ]; then
        echo "  ok   $script - $label"
    else
        echo "  FAIL $script - $label (got: ${out:-<silence>})"
        fail=1
    fi
}

for f in rapido-go.sh; do
    # Fresh server: no .env at all.
    run_case "$f" "no .env yet" ':'
    # After generate_env: .env exists, but has no token line in it - this is
    # the state a real public install reaches, and the one that used to die.
    run_case "$f" ".env exists without a token line" 'printf "POSTGRES_PASSWORD=x\nSUDO_PASSWORD=y\n" > "$APP_DIR/.env"'
done

# ── panel installer helpers ──────────────────────────────────────────────────
PANEL_SRC="$(prepare rapido-go.sh)"

# A working Python (a bare `python3` can be a Windows Store stub that exists
# but does not run), and whether this filesystem keeps chmod modes.
PY=""
for p in python3 python; do
    if "$p" -c 'pass' >/dev/null 2>&1; then PY="$p"; break; fi
done
probe="$WORK/chmod-probe"; : > "$probe"; chmod 600 "$probe"
CHMOD_OK=0; [ "$(stat -c %a "$probe" 2>/dev/null)" = "600" ] && CHMOD_OK=1

# run_unit <code>: run bash code with rapido-go.sh sourced, errexit on.
run_unit() {
    RAPIDO_GO_APP_DIR="$WORK/app" bash -c 'set -euo pipefail; . "$1" >/dev/null 2>&1; eval "$2"' _ "$PANEL_SRC" "$1" 2>&1
}

# unit <label> <expected stdout> <code>
unit() {
    local out
    out="$(run_unit "$3")" || true
    if [ "$out" = "$2" ]; then
        echo "  ok   rapido-go.sh - $1"
    else
        echo "  FAIL rapido-go.sh - $1"
        echo "       expected: $2"
        echo "       got:      $out"
        fail=1
    fi
}

# unit_has <label> <needle> <code>: stdout must contain the fixed string.
unit_has() {
    local out
    out="$(run_unit "$3")" || true
    if printf '%s\n' "$out" | grep -qF -- "$2"; then
        echo "  ok   rapido-go.sh - $1"
    else
        echo "  FAIL rapido-go.sh - $1 (missing: $2)"
        printf '%s\n' "$out" | sed 's/^/       | /'
        fail=1
    fi
}

# unit_lacks <label> <needle> <code>: stdout must NOT contain the fixed string.
unit_lacks() {
    local out
    out="$(run_unit "$3")" || true
    if printf '%s\n' "$out" | grep -qF -- "$2"; then
        echo "  FAIL rapido-go.sh - $1 (should not contain: $2)"
        printf '%s\n' "$out" | sed 's/^/       | /'
        fail=1
    else
        echo "  ok   rapido-go.sh - $1"
    fi
}

# unit_fails <label> <code>: the code must exit non-zero.
unit_fails() {
    if run_unit "$2" >/dev/null; then
        echo "  FAIL rapido-go.sh - $1 (expected a non-zero exit)"
        fail=1
    else
        echo "  ok   rapido-go.sh - $1"
    fi
}

# ---- flag parsing ----
unit "install flags are all parsed" \
    'a.example.com|b.example.com,c.example.com|s.example.com|bob|S3cret pass|ghp_abc123|1|1' \
    'parse_install_args --domain a.example.com --extra-domains b.example.com,c.example.com --sub-domain s.example.com --admin-user bob --admin-pass "S3cret pass" --token ghp_abc123 --yes --skip-preflight
     echo "$RAPIDO_DOMAIN|$RAPIDO_EXTRA_DOMAINS|$RAPIDO_SUB_DOMAIN|$RAPIDO_ADMIN_USER|$RAPIDO_ADMIN_PASS|$RAPIDO_REPO_TOKEN|$RAPIDO_YES|$RAPIDO_SKIP_PREFLIGHT"'
unit "--flag=value form is accepted" \
    'x.example.com|s.example.com' \
    'parse_install_args --domain=x.example.com --sub-domain=s.example.com; echo "$RAPIDO_DOMAIN|$RAPIDO_SUB_DOMAIN"'
unit "a flag overrides the environment" \
    'flag.example.com' \
    'RAPIDO_DOMAIN=env.example.com; parse_install_args --domain flag.example.com; echo "$RAPIDO_DOMAIN"'
unit "the environment is used when no flag is given" \
    'env.example.com' \
    'RAPIDO_DOMAIN=env.example.com; parse_install_args --yes; echo "$RAPIDO_DOMAIN"'
unit_fails "an unknown install option is refused" 'parse_install_args --bogus'
unit_has   "the unknown option is named in the error" 'Unknown option for install: --bogus' 'parse_install_args --bogus'
unit_fails "a flag with no value is refused" 'parse_install_args --domain'

# ---- domains ----
unit "host list is lowercased, trimmed and de-duplicated" \
    'a.example.com,b.example.com' \
    'normalize_host_list "A.Example.com, b.example.com ,a.example.com,"'
unit "a URL is reduced to its host" \
    'panel.example.com' \
    'normalize_host_list "https://Panel.example.com:8443/dashboard/"'
unit "an empty list stays empty" '' 'normalize_host_list ""'
unit_fails "a bad host name is refused" 'normalize_host_list "good.example.com,not_a_host"'
unit_fails "a bare IP is not a valid domain" '_valid_host 203.0.113.7'
unit_fails "a single label is not a valid domain" '_valid_host localhost'
unit "a normal domain is valid" 'yes' '_valid_host sub.panel-1.example.com && echo yes'
unit "extra/sub lists drop names that already have a site block" \
    'a.example.com|b.example.com|s.example.com' \
    'RAPIDO_DOMAIN=A.example.com RAPIDO_EXTRA_DOMAINS="a.example.com,b.example.com" RAPIDO_SUB_DOMAIN="b.example.com,s.example.com"
     finalize_domains; echo "$RAPIDO_DOMAIN|$RAPIDO_EXTRA_DOMAINS|$RAPIDO_SUB_DOMAIN"'
unit "all_domains lists panel, extra, then subscription names" \
    'a.example.com,b.example.com,s.example.com' \
    'RAPIDO_DOMAIN=a.example.com RAPIDO_EXTRA_DOMAINS=b.example.com RAPIDO_SUB_DOMAIN=s.example.com; all_domains'
unit "admin password: a single quote is refused" \
    'refused' \
    '_valid_admin_pass "it'"'"'s-bad-1" && echo accepted || echo refused'
unit "admin password: a normal one passes" 'ok' '_valid_admin_pass "Good-pass-123" && echo ok'
unit_fails "admin password: too short is refused" '_valid_admin_pass short'

# ---- generated password ----
unit "generated password is 22 letters/digits" \
    'ok' \
    'p="$(gen_password)"; [ "${#p}" -eq 22 ] && case "$p" in *[!A-Za-z0-9]*) echo bad ;; *) echo ok ;; esac'
unit "two generated passwords differ" \
    'different' \
    '[ "$(gen_password)" != "$(gen_password)" ] && echo different'

# ---- sizing ----
unit "postgres tuning: 512 MB machine is clamped up"    '128MB 512MB'   'pg_tuning_for_ram_mb 512'
unit "postgres tuning: 1 GB machine"                    '256MB 614MB'   'pg_tuning_for_ram_mb 1024'
unit "postgres tuning: 4 GB machine"                    '1024MB 2457MB' 'pg_tuning_for_ram_mb 4096'
unit "postgres tuning: 16 GB machine is clamped down"   '2048MB 8192MB' 'pg_tuning_for_ram_mb 16384'
unit "postgres tuning: unknown RAM falls back to 2 GB"  '512MB 1228MB'  'pg_tuning_for_ram_mb 0'

# ---- preflight arithmetic ----
unit "RAM below the minimum fails"        'fail'    'level_for_value 700 900 1800'
unit "RAM between minimum and comfort warns" 'warn' 'level_for_value 1024 900 1800'
unit "plenty of RAM is ok"                'ok'      'level_for_value 4096 900 1800'
unit "a non-number is unknown, not a crash" 'unknown' 'level_for_value "" 900 1800'
unit "x86_64 is amd64"      'amd64' 'normalize_arch x86_64'
unit "aarch64 is arm64"     'arm64' 'normalize_arch aarch64'
unit "armv7l is unsupported" ''     'normalize_arch armv7l'
unit "IPv4 validation"      'ok|bad|bad|bad' \
    '_valid_ipv4 203.0.113.7 && printf ok; printf "|"; _valid_ipv4 999.1.1.1 || printf bad; printf "|"; _valid_ipv4 1.2.3 || printf bad; printf "|"; _valid_ipv4 a.b.c.d || printf bad'
unit "preflight failures become warnings with --skip-preflight" \
    'continued' \
    'RAPIDO_SKIP_PREFLIGHT=1; preflight_fail "too small" >/dev/null; echo continued'
unit_fails "a preflight failure stops the install" 'RAPIDO_SKIP_PREFLIGHT=0; preflight_fail "too small"'
unit "days until a date" '10' 'days_until_date "Jan 11 00:00:00 2030 GMT" "$(date -d "Jan 1 00:00:00 2030 GMT" +%s)"'
unit "days until a past date is negative" '-5' 'days_until_date "Jan 1 00:00:00 2030 GMT" "$(date -d "Jan 6 00:00:00 2030 GMT" +%s)"'

# Ports: only when python3 can open a listener to test against.
if [ -n "$PY" ]; then
    port="$("$PY" -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
    "$PY" -c "
import socket, time
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', $port)); s.listen(1); time.sleep(20)" &
    listener=$!
    sleep 1
    unit "port_in_use sees a listener" 'busy' "port_in_use $port && echo busy"
    kill "$listener" 2>/dev/null; wait "$listener" 2>/dev/null
    unit "port_in_use sees a free port" 'free' "port_in_use $port || echo free"
else
    echo "  skip rapido-go.sh - port_in_use (no working python)"
fi

# ---- .env reading ----
unit "env_get strips quotes and takes the last value" \
    'two|fallback' \
    'f="$(mktemp)"; printf "A=\"one\"\nA=\x27two\x27\n" > "$f"; printf "%s|" "$(env_get A "" "$f")"; env_get MISSING fallback "$f"'
unit "env_get on a missing file gives the default" 'dflt' 'env_get X dflt /nonexistent/file'

# ---- Caddyfile ----
CADDY_ONE='RAPIDO_DOMAIN=panel.example.com RAPIDO_EXTRA_DOMAINS="" RAPIDO_SUB_DOMAIN=""'
CADDY_MANY='RAPIDO_DOMAIN=panel.example.com RAPIDO_EXTRA_DOMAINS="panel2.example.com,panel3.example.com" RAPIDO_SUB_DOMAIN="sub.example.com,sub2.example.com"'
unit "caddyfile: single domain, compressed and proxied" \
    'panel.example.com {
	encode zstd gzip
	handle /api/internal/node-report {
		reverse_proxy backend:8000
	}
	handle {
		reverse_proxy panel:8000
	}
}' \
    "$CADDY_ONE"'; render_caddyfile'
unit_has   "caddyfile: extra domains share the panel block" 'panel.example.com, panel2.example.com, panel3.example.com {' "$CADDY_MANY"'; render_caddyfile'
unit_has   "caddyfile: subscription domains get their own block" 'sub.example.com, sub2.example.com {' "$CADDY_MANY"'; render_caddyfile'
unit_has   "caddyfile: subscription block only proxies subscriber paths" '@subscriber path / /sub/* /statics/* /health' "$CADDY_MANY"'; render_caddyfile'
unit_has   "caddyfile: subscription block answers 404 for the rest" 'respond "Not found" 404' "$CADDY_MANY"'; render_caddyfile'
unit_lacks "caddyfile: no subscription block without a subscription domain" '@subscriber' "$CADDY_ONE"'; render_caddyfile'
unit "caddyfile: both blocks compress" '2' "$CADDY_MANY"'; render_caddyfile | grep -c "encode zstd gzip"'
# (grep -c reads everything - `grep -q` would exit early and hand the writer a
# SIGPIPE, which pipefail turns into a failure.)
unit "caddyfile uses tabs for indentation" 'yes' "$CADDY_MANY"'; [ "$(render_caddyfile | grep -cP "^\t")" -gt 0 ] && echo yes'
unit "an existing caddyfile that differs is kept as a backup" \
    '1' \
    'APP_DIR="$(mktemp -d)"; RAPIDO_DOMAIN=new.example.com RAPIDO_EXTRA_DOMAINS="" RAPIDO_SUB_DOMAIN=""
     printf "old.example.com {\n}\n" > "$APP_DIR/Caddyfile"; write_caddyfile >/dev/null 2>&1
     ls "$APP_DIR"/Caddyfile.bak-* | wc -l'

# A hand-written Caddyfile (global options, a snippet, comma lists, a scheme, a
# port-only address) - doctor must see exactly the real site names in it.
HAND_CADDY='APP_DIR="$(mktemp -d)"; cat > "$APP_DIR/Caddyfile" <<CF
{
	email admin@example.com
}
(common) {
	encode gzip
}
# comment {
panel.example.com, panel2.example.com {
	import common
	reverse_proxy panel:8000
}
https://sub.example.com,sub2.example.com {
	reverse_proxy panel:8000
}
:8080 {
	respond "x"
}
CF
'
unit "caddyfile_hosts finds the real site names in a hand-written file" \
    'panel.example.com panel2.example.com sub.example.com sub2.example.com' \
    "$HAND_CADDY"'caddyfile_hosts | tr "\n" " " | sed "s/ \$//"'
unit "doctor_names merges .env names with the Caddyfile, primary first, no duplicates" \
    'a.example.com panel.example.com panel2.example.com sub.example.com sub2.example.com' \
    "$HAND_CADDY"'RAPIDO_DOMAIN=a.example.com RAPIDO_EXTRA_DOMAINS=panel.example.com RAPIDO_SUB_DOMAIN=""; doctor_names'
unit "caddyfile_hosts with no Caddyfile prints nothing" '' 'APP_DIR="$(mktemp -d)"; caddyfile_hosts'

# ---- .env ----
ENV_ONE='RAPIDO_DOMAIN=panel.example.com RAPIDO_EXTRA_DOMAINS="" RAPIDO_SUB_DOMAIN=""'
ENV_SUB='RAPIDO_DOMAIN=panel.example.com RAPIDO_EXTRA_DOMAINS="panel2.example.com" RAPIDO_SUB_DOMAIN="sub.example.com,sub2.example.com"'
unit_has   "env: domain" 'RAPIDO_DOMAIN="panel.example.com"' "$ENV_ONE"'; render_env dbpw admin pw12345678 256MB 614MB 203.0.113.7'
unit_has   "env: postgres tuning" 'POSTGRES_SHARED_BUFFERS="256MB"' "$ENV_ONE"'; render_env dbpw admin pw12345678 256MB 614MB 203.0.113.7'
unit_has   "env: effective cache size" 'POSTGRES_EFFECTIVE_CACHE_SIZE="614MB"' "$ENV_ONE"'; render_env dbpw admin pw12345678 256MB 614MB 203.0.113.7'
unit_has   "env: the password is single-quoted so compose does not expand \$" "SUDO_PASSWORD='pa\$\$word1'" "$ENV_ONE"'; render_env dbpw admin '"'"'pa$$word1'"'"' 256MB 614MB 203.0.113.7'
unit_has   "env: subscription prefix is the panel domain by default" 'XRAY_SUBSCRIPTION_URL_PREFIX="https://panel.example.com"' "$ENV_ONE"'; render_env dbpw admin pw12345678 256MB 614MB 203.0.113.7'
unit_has   "env: a single address leaves the prefixes list blank" 'XRAY_SUBSCRIPTION_URL_PREFIXES=""' "$ENV_ONE"'; render_env dbpw admin pw12345678 256MB 614MB 203.0.113.7'
unit_has   "env: subscription prefix moves to the subscription domain" 'XRAY_SUBSCRIPTION_URL_PREFIX="https://sub.example.com"' "$ENV_SUB"'; render_env dbpw admin pw12345678 256MB 614MB 203.0.113.7'
unit_has   "env: several subscription domains are listed in order" 'XRAY_SUBSCRIPTION_URL_PREFIXES="https://sub.example.com,https://sub2.example.com"' "$ENV_SUB"'; render_env dbpw admin pw12345678 256MB 614MB 203.0.113.7'
unit "env: it parses as a shell-style env file and keeps the password intact" \
    'pa$$word1|panel.example.com' \
    "$ENV_ONE"'; f="$(mktemp)"; render_env dbpw admin '"'"'pa$$word1'"'"' 256MB 614MB 203.0.113.7 > "$f"
     printf "%s|%s" "$(sed -n "s/^SUDO_PASSWORD=\x27\(.*\)\x27\$/\1/p" "$f")" "$(env_get RAPIDO_DOMAIN "" "$f")"'

# ---- the GitHub token never appears in a process argument ----
TOKEN_STUB='curl() { echo "ARGS:$*"; cat; }; _curl_gh "ghp_Secret123" -s https://example.invalid/x'
unit_has   "the token reaches curl as a config header on stdin" 'header = "Authorization: Bearer ghp_Secret123"' "$TOKEN_STUB"
unit "the token is not in curl's arguments" 'ARGS:-K - -s https://example.invalid/x' "$TOKEN_STUB"' | grep "^ARGS:"'
unit "a token with quote/newline characters cannot inject config" 'header = "Authorization: Bearer ghp_xurlhttpevilexample"' \
    'curl() { cat; }; _curl_gh "$(printf "ghp_x\"\nurl = \"http://evil.example")" -s https://example.invalid/x'
unit "no token means no auth header at all" 'ARGS:-s https://example.invalid/x' \
    'curl() { echo "ARGS:$*"; }; _curl_gh "" -s https://example.invalid/x'

# ---- lightweight fetch ----
unit "lite fetch places the files and rejects nothing valid" \
    'ok|1|1' \
    'APP_DIR="$(mktemp -d)"
     fetch_repo_file() { case "$1" in
         docker-compose.prod.yml) printf "services:\n  a: {}\n" > "$2" ;;
         rapido-go.sh) printf "#!/usr/bin/env bash\ntrue\n" > "$2" ;;
         *) : > "$2" ;; esac; }
     _curl_gh() { return 1; }
     fetch_source_lite >/dev/null 2>&1
     printf "ok|%s|%s" "$(grep -c "^services:" "$APP_DIR/docker-compose.prod.yml")" "$([ -x "$APP_DIR/rapido-go.sh" ] && echo 1)"'
unit "lite fetch refuses an error page and leaves the install alone" \
    'kept' \
    'APP_DIR="$(mktemp -d)"; printf "services:\n  keep: {}\n" > "$APP_DIR/docker-compose.prod.yml"
     fetch_repo_file() { printf "<html>captive portal</html>\n" > "$2"; }
     _curl_gh() { return 1; }
     ( fetch_source_lite ) >/dev/null 2>&1 || true
     grep -q "keep:" "$APP_DIR/docker-compose.prod.yml" && echo kept'
unit "lite fetch fails cleanly when a download fails" \
    'failed|no leftovers' \
    'APP_DIR="$(mktemp -d)"
     fetch_repo_file() { return 22; }
     _curl_gh() { return 1; }
     ( fetch_source_lite ) >/dev/null 2>&1 && echo unexpected || printf "failed|"
     [ -z "$(ls -A "$APP_DIR" | grep "^.fetch" || true)" ] && echo "no leftovers"'
unit "a directory without .git is a lite install" 'lite' 'APP_DIR="$(mktemp -d)"; detect_install_mode'
unit "a git checkout keeps using git" 'git' 'APP_DIR="$(mktemp -d)"; mkdir "$APP_DIR/.git"; detect_install_mode'
unit "building locally needs the full checkout" 'git' 'APP_DIR="$(mktemp -d)"; RAPIDO_BUILD_LOCALLY=1 detect_install_mode'

# ---- parallel image pull ----
unit "pull_images reports only the image that failed" \
    'b:2' \
    'image_list() { printf "a:1\nb:2\nc:3\n"; }
     docker() { if [ "$*" = "pull -q b:2" ]; then echo "denied: not allowed" >&2; return 1; fi; return 0; }
     pull_images >/dev/null 2>&1; echo "$PULL_FAILED"'
unit "pull_images pulls concurrently" \
    'fast' \
    'image_list() { printf "a:1\nb:2\nc:3\nd:4\n"; }
     docker() { sleep 1; return 0; }
     s=$SECONDS; pull_images >/dev/null 2>&1; [ $((SECONDS - s)) -lt 3 ] && echo fast'
unit "pull_images leaves the panel image to the local build" \
    'other:1' \
    'image_list() { printf "ghcr.io/x/rapido-go-panel:latest\nrapido-go-panel:private\nother:1\n"; }
     docker() { echo "$*" >> "$WORK_LOG"; return 0; }
     WORK_LOG="$(mktemp)"; RAPIDO_BUILD_LOCALLY=1 pull_images >/dev/null 2>&1; sed "s/pull -q //" "$WORK_LOG"'

unit "the private alias pulls a selected registry image and preserves its predecessor" \
    'pull -q ghcr.io/rapidodev/rapido-go-panel:release-test
image inspect rapido-go-panel:private
tag rapido-go-panel:private rapido-go-panel:previous
tag ghcr.io/rapidodev/rapido-go-panel:release-test rapido-go-panel:private' \
    'WORK_LOG="$(mktemp)"; docker() { echo "$*" >> "$WORK_LOG"; return 0; }
     RAPIDO_PRIVATE_IMAGE_SOURCE=ghcr.io/rapidodev/rapido-go-panel:release-test pull_image rapido-go-panel:private >/dev/null
     grep -v "^image inspect --format " "$WORK_LOG"'
unit "the private alias defaults to the published panel image" \
    'pull -q ghcr.io/rapidodev/rapido-go-panel:latest' \
    'WORK_LOG="$(mktemp)"; docker() { echo "$*" >> "$WORK_LOG"; return 0; }
     APP_DIR="$(mktemp -d)"; unset RAPIDO_PRIVATE_IMAGE_SOURCE
     pull_image rapido-go-panel:private >/dev/null
     head -n 1 "$WORK_LOG"'
unit "a failed private image download leaves the local alias untouched" \
    'pull -q ghcr.io/rapidodev/rapido-go-panel:release-test' \
    'WORK_LOG="$(mktemp)"; docker() { echo "$*" >> "$WORK_LOG"; return 1; }
     if RAPIDO_PRIVATE_IMAGE_SOURCE=ghcr.io/rapidodev/rapido-go-panel:release-test pull_image rapido-go-panel:private >/dev/null; then exit 1; fi
     cat "$WORK_LOG"'
unit_fails "the private alias cannot be its own registry source" \
    'RAPIDO_PRIVATE_IMAGE_SOURCE=rapido-go-panel:private pull_image rapido-go-panel:private'
unit "a failed image backup prevents replacing the private alias" \
    'image inspect rapido-go-panel:private
tag rapido-go-panel:private rapido-go-panel:previous' \
    'WORK_LOG="$(mktemp)"; docker() { echo "$*" >> "$WORK_LOG"; [ "$1" != tag ]; }
     if tag_private_panel_image ghcr.io/rapidodev/rapido-go-panel:release-test; then exit 1; fi
     grep -v "^image inspect --format " "$WORK_LOG"'

unit "legacy private edition metadata is recognized" 'private' \
    'docker() { case "$*" in *org.opencontainers.image.version*) echo gre-frp-private ;; esac; }
     image_has_private_features rapido-go-panel:private && echo private'
unit "a public candidate cannot overwrite a private installation" 'kept' \
    'image_has_private_features() { [ "$1" = rapido-go-panel:private ]; }
     docker() { case "$1" in tag) echo UNEXPECTED_TAG ;; esac; return 0; }
     if tag_private_panel_image ghcr.io/rapidodev/rapido-go-panel:public >/dev/null 2>&1; then exit 1; fi
     echo kept'
unit "recovery keeps the original private backup when the active image is public" \
    'tag rapido-go-panel:private rapido-go-panel:previous-public
tag private-candidate rapido-go-panel:private' \
    'WORK_LOG="$(mktemp)"; image_has_private_features() { [ "$1" = rapido-go-panel:previous ] || [ "$1" = private-candidate ]; }
     docker() { echo "$*" >> "$WORK_LOG"; return 0; }
     tag_private_panel_image private-candidate; grep "^tag " "$WORK_LOG"'
unit_fails "building a private edition without its sources fails closed" \
    'APP_DIR="$(mktemp -d)"; mkdir -p "$APP_DIR/docker"; : > "$APP_DIR/docker/Dockerfile.panel"
     private_installation() { return 0; }; source_version() { echo test; }
     docker() { echo UNEXPECTED_BUILD; return 0; }; build_local_image'
unit "available private add-on sources enable the private build tag" 'private-build' \
    'APP_DIR="$(mktemp -d)"; mkdir -p "$APP_DIR/docker" "$APP_DIR/internal/httpapi" "$APP_DIR/web/src/private" "$APP_DIR/internal/mullvadexits"
     : > "$APP_DIR/docker/Dockerfile.panel"; : > "$APP_DIR/internal/httpapi/ext_private_exits.go"; : > "$APP_DIR/web/src/private/index.tsx"
     touch "$APP_DIR/internal/mullvadexits/"{service.go,decision.go,relays.go,exits.py}
     WORK_LOG="$(mktemp)"; docker() { echo "$*" >> "$WORK_LOG"; return 0; }
     private_installation() { return 1; }; source_version() { echo test; }; image_list() { :; }
     build_local_image >/dev/null
     grep -q -- "--build-arg GO_TAGS=private" "$WORK_LOG" && grep -q -- "--build-arg VERSION=test-private" "$WORK_LOG" && echo private-build'
unit_fails "a private add-on without its service dependency fails before Docker build" \
    'APP_DIR="$(mktemp -d)"; mkdir -p "$APP_DIR/docker" "$APP_DIR/internal/httpapi" "$APP_DIR/web/src/private"
     touch "$APP_DIR/docker/Dockerfile.panel" "$APP_DIR/internal/httpapi/ext_private_exits.go" "$APP_DIR/web/src/private/index.tsx"
     private_installation() { return 1; }; source_version() { echo test; }
     docker() { echo UNEXPECTED_BUILD; return 0; }; build_local_image'
unit "a local build also updates the private deployment alias" \
    'tag rapido-go-panel:private rapido-go-panel:previous
tag ghcr.io/rapidodev/rapido-go-panel:latest rapido-go-panel:private' \
    'APP_DIR="$(mktemp -d)"; mkdir -p "$APP_DIR/docker"; : > "$APP_DIR/docker/Dockerfile.panel"
     WORK_LOG="$(mktemp)"; docker() { echo "$*" >> "$WORK_LOG"; return 0; }
     source_version() { echo test; }; image_list() { echo rapido-go-panel:private; }
     build_local_image >/dev/null; grep "^tag " "$WORK_LOG"'

# ---- what happens when a pull fails ----
unit "a failed refresh of an image that is already here is only a warning" 'ready' \
    'pull_images() { PULL_FAILED="postgres:16-alpine"; }; docker() { return 0; }
     APP_DIR="$(mktemp -d)"; obtain_images >/dev/null 2>&1 && echo ready'
unit_fails "a failed pull of an image that is not here stops the install" \
    'pull_images() { PULL_FAILED="postgres:16-alpine"; }; docker() { return 1; }
     APP_DIR="$(mktemp -d)"; obtain_images'
unit_fails "a lite install with no panel image stops with instructions" \
    'pull_images() { PULL_FAILED="ghcr.io/x/rapido-go-panel:latest"; }; docker() { return 0; }
     APP_DIR="$(mktemp -d)"; obtain_images'
unit "a git install falls back to building the panel image" 'BUILD' \
    'pull_images() { PULL_FAILED="ghcr.io/x/rapido-go-panel:latest"; }; docker() { return 0; }
     build_local_image() { echo BUILD; }
     APP_DIR="$(mktemp -d)"; mkdir "$APP_DIR/.git"; obtain_images 2>&1 | grep -x BUILD'
unit "RAPIDO_BUILD_LOCALLY builds instead of pulling the panel image" 'BUILD' \
    'pull_images() { PULL_FAILED=""; }; build_local_image() { echo BUILD; }
     APP_DIR="$(mktemp -d)"; RAPIDO_BUILD_LOCALLY=1 obtain_images 2>&1 | grep -x BUILD'

# ---- what the shipped files must keep ----
unit "the backend gets the subscription address, for the links the Telegram console hands out" '2' \
    'sed -n "/^  backend:/,/^  caddy:/p" docker-compose.prod.yml | grep -cE "^      XRAY_SUBSCRIPTION_URL_PREFIX(ES)?:"'
unit "both node builds include what REALITY needs" '2' \
    'grep -c "go build -trimpath -tags with_utls" docker/Dockerfile.panel'

# ---- local compose overrides ----
unit "compose uses only the shipped file when there is no custom one" '-f docker-compose.prod.yml' \
    'APP_DIR="$(mktemp -d)"; compose_file_args | paste -sd" " -'
unit "compose merges docker-compose.custom.yml when it exists" '-f docker-compose.prod.yml -f docker-compose.custom.yml' \
    'APP_DIR="$(mktemp -d)"; : > "$APP_DIR/docker-compose.custom.yml"; compose_file_args | paste -sd" " -'

# ---- update decision ----
unit "update is needed when the compose file changed" 'needed' \
    'COMPOSE_CHANGED=1; images_fingerprint() { echo same; }; update_needed same && echo needed'
unit "update is needed when images changed" 'needed' \
    'COMPOSE_CHANGED=0; images_fingerprint() { echo new; }; missing_services() { echo; }; update_needed old && echo needed'
unit "update is skipped when nothing changed" 'skipped' \
    'COMPOSE_CHANGED=0; images_fingerprint() { echo same; }; missing_services() { echo; }
     compose() { case "$1" in ps) echo abc123 ;; esac; }
     image_list() { echo ghcr.io/x/rapido-go-panel:latest; }
     docker() { case "$1" in inspect) echo sha256:aaa ;; image) echo sha256:aaa ;; esac; }
     update_needed same || echo skipped'
unit "update is needed when the running container is on an older image" 'needed' \
    'COMPOSE_CHANGED=0; images_fingerprint() { echo same; }; missing_services() { echo; }
     compose() { case "$1" in ps) echo abc123 ;; esac; }
     image_list() { echo ghcr.io/x/rapido-go-panel:latest; }
     docker() { case "$1" in inspect) echo sha256:old ;; image) echo sha256:new ;; esac; }
     update_needed same && echo needed'

# ---- token handling ----
unit "a public repository drops the token and does not store it" \
    'dropped|0|' \
    'github_status() { echo 200; }; ghcr_image_visibility() { echo public; }
     APP_DIR="$(mktemp -d)"; RAPIDO_REPO_TOKEN=ghp_stale
     resolve_repo_access strict >/dev/null 2>&1
     printf "dropped|%s|%s" "$TOKEN_NEEDED" "$RAPIDO_REPO_TOKEN"'
unit "a public repository with a private image keeps the token" \
    'kept|1' \
    'github_status() { echo 200; }; ghcr_image_visibility() { echo private; }
     APP_DIR="$(mktemp -d)"; RAPIDO_REPO_TOKEN=ghp_good
     resolve_repo_access strict >/dev/null 2>&1
     printf "kept|%s" "$TOKEN_NEEDED"'
unit "an image that cannot be checked keeps the token that is already in hand" \
    'kept|1' \
    'github_status() { echo 200; }; ghcr_image_visibility() { echo unknown; }
     APP_DIR="$(mktemp -d)"; RAPIDO_REPO_TOKEN=ghp_good
     resolve_repo_access strict >/dev/null 2>&1
     printf "kept|%s" "$TOKEN_NEEDED"'
unit "a rate-limited GitHub with no token is treated as public" \
    '0' \
    'github_status() { echo 403; }; ghcr_image_visibility() { echo public; }
     APP_DIR="$(mktemp -d)"; RAPIDO_REPO_TOKEN=""
     resolve_repo_access strict >/dev/null 2>&1
     echo "$TOKEN_NEEDED"'
unit_fails "a private repository with no token and no terminal stops with a message" \
    'github_status() { echo 404; }; APP_DIR="$(mktemp -d)"; RAPIDO_REPO_TOKEN=""; resolve_repo_access strict'
unit "a rejected token is refused" 'refused' \
    'github_status() { if [ -z "$1" ]; then echo 404; else echo 401; fi; }
     APP_DIR="$(mktemp -d)"; RAPIDO_REPO_TOKEN=ghp_bad
     ( resolve_repo_access strict ) >/dev/null 2>&1 || echo refused'
unit "update mode never stops on an unusable token" 'continued' \
    'github_status() { if [ -z "$1" ]; then echo 404; else echo 401; fi; }
     APP_DIR="$(mktemp -d)"; RAPIDO_REPO_TOKEN=ghp_bad
     resolve_repo_access lenient >/dev/null 2>&1; echo continued'
unit "a new token replaces a saved one" 'ghp_new' \
    'APP_DIR="$(mktemp -d)"; printf "A=1\nRAPIDO_REPO_TOKEN=\"ghp_old\"\nB=2\n" > "$APP_DIR/.env"
     TOKEN_NEEDED=1; RAPIDO_REPO_TOKEN=ghp_new; save_token; env_get RAPIDO_REPO_TOKEN "" "$APP_DIR/.env"'
if [ "$CHMOD_OK" = "1" ]; then
    unit "the saved token file stays private" '600' \
        'APP_DIR="$(mktemp -d)"; printf "A=1\n" > "$APP_DIR/.env"; chmod 644 "$APP_DIR/.env"
         TOKEN_NEEDED=1; RAPIDO_REPO_TOKEN=ghp_new; save_token; stat -c %a "$APP_DIR/.env"'
else
    echo "  skip rapido-go.sh - saved token file mode (this filesystem ignores chmod)"
fi

# ---- confirm / no terminal ----
unit "confirm answers the default when there is no terminal" 'yes|no' \
    'confirm "go?" y </dev/null && printf yes; printf "|"; confirm "go?" n </dev/null && printf yes || printf no'
unit_has "an existing .env keeps its admin login and says so" 'admin login is kept' \
    'APP_DIR="$(mktemp -d)"; printf "RAPIDO_DOMAIN=\"a.example.com\"\nSUDO_PASSWORD=x\n" > "$APP_DIR/.env"
     RAPIDO_DOMAIN=""; RAPIDO_ADMIN_PASS="whatever-123"; resolve_inputs </dev/null 2>&1'
unit_has "a different --domain over an existing .env is called out" 'names a different domain' \
    'APP_DIR="$(mktemp -d)"; printf "RAPIDO_DOMAIN=\"a.example.com\"\nSUDO_PASSWORD=x\n" > "$APP_DIR/.env"
     RAPIDO_DOMAIN="b.example.com"; resolve_inputs </dev/null 2>&1'
unit "an existing .env yields no new password" 'none' \
    'APP_DIR="$(mktemp -d)"; printf "RAPIDO_DOMAIN=\"a.example.com\"\nSUDO_PASSWORD=x\n" > "$APP_DIR/.env"
     RAPIDO_DOMAIN=""; resolve_inputs </dev/null >/dev/null 2>&1; echo "${ADMIN_PASSWORD:-none}"'
unit_fails "install with no domain and no terminal stops instead of hanging" \
    'RAPIDO_DOMAIN=""; APP_DIR="$(mktemp -d)"; resolve_inputs </dev/null'

# ---- the panel answering decides ----
unit "wait_healthy fails when the panel never answers" 'failed' \
    'RAPIDO_DOMAIN=a.example.com; compose() { return 1; }; sleep() { :; }; curl() { return 0; }
     if wait_healthy >/dev/null 2>&1; then echo up; else echo failed; fi'
unit "wait_healthy stays a warning when only the public name does not answer" 'up' \
    'RAPIDO_DOMAIN=a.example.com; compose() { return 0; }; sleep() { :; }; curl() { return 1; }
     if wait_healthy >/dev/null 2>&1; then echo up; else echo failed; fi'
INSTALL_STUBS='for f in require_root banner preflight_system ensure_prereqs resolve_inputs preflight_dns resolve_repo_access ensure_docker ensure_compose fetch_source generate_env write_caddyfile save_token install_command ensure_registry_login obtain_images compose verify_stack; do eval "$f() { :; }"; done
     print_summary() { echo SUMMARY; }; RAPIDO_DOMAIN=a.example.com'
unit_has "an install whose panel never answers still shows the one-time password" 'SUMMARY' \
    "$INSTALL_STUBS"'; wait_healthy() { return 1; }; cmd_install 2>&1'
unit_fails "an install whose panel never answers does not report success" \
    "$INSTALL_STUBS"'; wait_healthy() { return 1; }; cmd_install'

# ---- backup and restore ----
# A stand-in for `docker compose`: records every call, answers pg_dump with a
# complete dump and keeps what would have reached psql. COMPOSE_FAIL is a fragment
# of the one call that fails.
DB_STUBS='require_root() { :; }
     APP_DIR="$(mktemp -d)"; DATA_DIR="$(mktemp -d)"; LOG="$(mktemp)"; STREAM="$(mktemp)"; COMPOSE_FAIL=""
     : > "$APP_DIR/.env"
     good_dump() { printf "%s\n" "-- PostgreSQL database dump" "CREATE TABLE public.t (a int);" "--" "-- PostgreSQL database dump complete" "--" | gzip > "$1"; }
     compose() {
         echo "compose $*" >> "$LOG"
         if [ -n "$COMPOSE_FAIL" ]; then case "$*" in *"$COMPOSE_FAIL"*) return 1 ;; esac; fi
         case "$*" in
             *pg_dump*) printf "%s\n" "-- PostgreSQL database dump" "-- the current database" "-- PostgreSQL database dump complete" ;;
             *psql*) cat > "$STREAM" ;;
         esac
     }
     SRC="$DATA_DIR/chosen.sql.gz"; good_dump "$SRC"
     steps() { sed -e "s/.*pg_dump.*/DUMP/" -e "s/.*psql.*/PSQL/" -e "s/.*FLUSHALL.*/FLUSH/" -e "s/^compose //" "$LOG"; }'

unit "a complete dump passes" 'ok' \
    "$DB_STUBS"'; check_dump_file "$SRC" && echo ok'
unit "a dump cut off in the middle is refused" 'refused' \
    "$DB_STUBS"'; f="$(mktemp)"; seq 1 200000 | gzip > "$f.gz"; head -c 100000 "$f.gz" > "$f"
     check_dump_file "$f" 2>/dev/null || echo refused'
unit "an intact gzip without the closing line is refused" 'refused' \
    "$DB_STUBS"'; f="$(mktemp)"; printf "CREATE TABLE t (a int);\n" | gzip > "$f"
     check_dump_file "$f" 2>/dev/null || echo refused'
unit "the closing words in the middle of the data do not count" 'refused' \
    "$DB_STUBS"'; f="$(mktemp)"; { echo "-- PostgreSQL database dump complete"; seq 1 5000; } | gzip > "$f"
     check_dump_file "$f" 2>/dev/null || echo refused'
unit "a file that is not gzip at all is refused" 'refused' \
    "$DB_STUBS"'; f="$(mktemp)"; echo "not a dump" > "$f"; check_dump_file "$f" 2>/dev/null || echo refused'
unit "a large healthy dump is not refused over a closed pipe" 'ok' \
    "$DB_STUBS"'; f="$(mktemp)"; { seq 1 2000000; echo "-- PostgreSQL database dump complete"; echo "--"; } | gzip > "$f"
     check_dump_file "$f" && echo ok'

unit "a failed pg_dump leaves nothing behind and keeps what was at that path" 'kept|' \
    "$DB_STUBS"'; COMPOSE_FAIL=pg_dump; dest="$DATA_DIR/x.sql.gz"; echo kept > "$dest"
     ( cmd_backup "$dest" ) >/dev/null 2>&1 || true
     printf "%s|%s" "$(cat "$dest")" "$(ls "$DATA_DIR" | grep partial || true)"'
unit_fails "an empty dump does not count as a backup" \
    "$DB_STUBS"'; compose() { :; }; cmd_backup "$DATA_DIR/x.sql.gz"'
if [ "$CHMOD_OK" = "1" ]; then
    unit "a backup is private whatever the umask and whatever was at that path" '600' \
        "$DB_STUBS"'; umask 022; dest="$DATA_DIR/x.sql.gz"; echo old > "$dest"; chmod 644 "$dest"
         cmd_backup "$dest" >/dev/null 2>&1; stat -c %a "$dest"'
    unit "a backup makes the data directory and the older dumps private" '700|600|600' \
        "$DB_STUBS"'; chmod 755 "$DATA_DIR"; chmod 644 "$SRC"; : > "$DATA_DIR/install-1.tar.gz"; chmod 644 "$DATA_DIR/install-1.tar.gz"
         cmd_backup >/dev/null 2>&1
         printf "%s|%s|%s" "$(stat -c %a "$DATA_DIR")" "$(stat -c %a "$SRC")" "$(stat -c %a "$DATA_DIR/install-1.tar.gz")"'
    unit "an install over an existing .env still makes the data directory private" '700|600' \
        "$DB_STUBS"'; chmod 755 "$DATA_DIR"; chmod 644 "$APP_DIR/.env"; generate_env >/dev/null 2>&1
         printf "%s|%s" "$(stat -c %a "$DATA_DIR")" "$(stat -c %a "$APP_DIR/.env")"'
else
    echo "  skip rapido-go.sh - dump and data directory modes (this filesystem ignores chmod)"
fi

unit "restore refuses a damaged file before anything is stopped" 'untouched' \
    "$DB_STUBS"'; f="$(mktemp)"; seq 1 200000 | gzip > "$f.gz"; head -c 100000 "$f.gz" > "$f"
     ( cmd_restore "$f" <<<yes ) >/dev/null 2>&1 || true
     [ -s "$LOG" ] || echo untouched'
unit "restore refuses a file with no closing line before anything is stopped" 'untouched' \
    "$DB_STUBS"'; f="$(mktemp)"; echo "CREATE TABLE t (a int);" | gzip > "$f"
     ( cmd_restore "$f" <<<yes ) >/dev/null 2>&1 || true
     [ -s "$LOG" ] || echo untouched'
unit "restore stops nothing without a yes" 'untouched' \
    "$DB_STUBS"'; ( cmd_restore "$SRC" <<<no ) >/dev/null 2>&1 || true
     [ -s "$LOG" ] || echo untouched'
unit_has "restore says the whole database is replaced" 'REPLACES THE WHOLE DATABASE' \
    "$DB_STUBS"'; ( cmd_restore "$SRC" <<<no ) 2>&1 || true'
unit_has "restore says what happens to everything since that backup" 'Everything changed since that backup was taken is lost' \
    "$DB_STUBS"'; ( cmd_restore "$SRC" <<<no ) 2>&1 || true'
unit_has "restore says where the safety backup goes" 'pre-restore-' \
    "$DB_STUBS"'; ( cmd_restore "$SRC" <<<no ) 2>&1 || true'
unit "restore stops, saves, replaces, empties the cache and starts again, in that order" \
    'stop panel backend
DUMP
PSQL
FLUSH
up -d panel backend' \
    "$DB_STUBS"'; ( cmd_restore "$SRC" <<<yes ) >/dev/null 2>&1; steps'
unit "restore leaves a complete safety backup" 'ok' \
    "$DB_STUBS"'; ( cmd_restore "$SRC" <<<yes ) >/dev/null 2>&1
     check_dump_file "$DATA_DIR"/pre-restore-*.sql.gz && echo ok'
unit "the restore replaces the schema first, in one transaction it only commits at the end" \
    'BEGIN;
SET client_min_messages = warning;
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
-- PostgreSQL database dump
CREATE TABLE public.t (a int);
--
-- PostgreSQL database dump complete
--

COMMIT;' \
    "$DB_STUBS"'; ( cmd_restore "$SRC" <<<yes ) >/dev/null 2>&1; cat "$STREAM"'
unit "a stream that breaks halfway never reaches COMMIT, and is reported" 'failed|0' \
    "$DB_STUBS"'; gunzip() { echo "half a dump"; return 1; }
     if replace_database "$SRC"; then printf "ok|"; else printf "failed|"; fi
     grep -c COMMIT "$STREAM" || true'
unit "a failed restore still starts the panel, and fails" \
    'exit 1
stop panel backend
DUMP
PSQL
up -d panel backend' \
    "$DB_STUBS"'; COMPOSE_FAIL=psql; ( cmd_restore "$SRC" <<<yes ) >/dev/null 2>&1 && echo "exit 0" || echo "exit 1"; steps'
unit "a failed safety backup replaces nothing and still starts the panel" \
    'exit 1
stop panel backend
DUMP
up -d panel backend' \
    "$DB_STUBS"'; COMPOSE_FAIL=pg_dump; ( cmd_restore "$SRC" <<<yes ) >/dev/null 2>&1 && echo "exit 0" || echo "exit 1"; steps'
unit "a panel that will not start again after a good restore is a failure" 'exit 1' \
    "$DB_STUBS"'; COMPOSE_FAIL="up -d"; ( cmd_restore "$SRC" <<<yes ) >/dev/null 2>&1 && echo "exit 0" || echo "exit 1"'
unit_has "that failure says where the database from before the restore is" 'as it was before this restore is in' \
    "$DB_STUBS"'; COMPOSE_FAIL="up -d"; ( cmd_restore "$SRC" <<<yes ) 2>&1 || true'
unit_has "a good restore says how to undo it" 'To undo this: rapido-go restore ' \
    "$DB_STUBS"'; ( cmd_restore "$SRC" <<<yes ) 2>&1'

# Killed halfway: the signal lands while the restore is still feeding psql.
restore_log="$(mktemp -p "$WORK")"
RAPIDO_GO_APP_DIR="$WORK/app" RESTORE_LOG="$restore_log" bash -c 'set -euo pipefail; . "$1" >/dev/null 2>&1; eval "$2"' _ "$PANEL_SRC" \
    "$DB_STUBS"'; LOG="$RESTORE_LOG"
     compose() { echo "compose $*" >> "$LOG"; case "$*" in *pg_dump*) printf "%s\n" "-- PostgreSQL database dump complete" ;; *psql*) sleep 2 ;; esac; }
     cmd_restore "$SRC" <<<yes' >/dev/null 2>&1 &
restore_pid=$!
for _ in $(seq 1 100); do
    grep -q psql "$restore_log" && break
    sleep 0.1
done
kill -TERM "$restore_pid" 2>/dev/null
wait "$restore_pid" 2>/dev/null
restore_rc=$?
if [ "$restore_rc" -eq 143 ] && [ "$(tail -n 1 "$restore_log")" = "compose up -d panel backend" ]; then
    echo "  ok   rapido-go.sh - a restore killed halfway still starts the panel"
else
    echo "  FAIL rapido-go.sh - a restore killed halfway still starts the panel (exit $restore_rc)"
    sed 's/^/       | /' "$restore_log"
    fail=1
fi

# ---- update ----
# The stand-ins keep the image the stack is configured for in a file: the pull
# happens in a function here, but the rollback reads it back later.
UPDATE_STUBS="$DB_STUBS"'
     for f in ensure_docker resolve_repo_access install_command ensure_registry_login follow_logs verify_stack; do eval "$f() { :; }"; done
     file_sum() { echo same; }; images_fingerprint() { echo fp; }; update_needed() { return 0; }; installed_version_label() { echo new; }
     image_list() { echo ghcr.io/x/rapido-go-panel:latest; }
     IMAGE_NOW="$(mktemp)"; echo sha256:old > "$IMAGE_NOW"; CONTAINER=cid
     for f in docker-compose.prod.yml rapido-go.sh Caddyfile; do echo before > "$APP_DIR/$f"; done
     fetch_source() { echo after > "$APP_DIR/docker-compose.prod.yml"; }
     obtain_images() { echo PULL >> "$LOG"; echo sha256:new > "$IMAGE_NOW"; }
     wait_healthy() { echo HEALTH >> "$LOG"; [ "${PANEL_ANSWERS:-1}" = 1 ]; }
     real_compose="$(declare -f compose)"; eval "${real_compose/compose ()/recorded_compose ()}"
     compose() { case "$*" in "ps -a -q panel") echo "$CONTAINER" ;; *) recorded_compose "$@" ;; esac; }
     docker() {
         echo "docker $*" >> "$LOG"
         case "$*" in
             "inspect --format {{.Image}} cid") echo sha256:old ;;
             "inspect --format {{.Config.Image}} cid") echo ghcr.io/x/rapido-go-panel:latest ;;
             "image inspect --format {{.Id}} "*) cat "$IMAGE_NOW" ;;
         esac
     }
     events() { grep -E "^(PULL|HEALTH|compose (stop|up)|docker tag|docker image prune)|pg_dump|psql|FLUSHALL" "$LOG" | sed -e "s/.*pg_dump.*/DUMP/" -e "s/.*psql.*/PSQL/" -e "s/.*FLUSHALL.*/FLUSH/" -e "s/^compose //" -e "s/^docker //"; }'

unit "a good update keeps the old image first and prunes only after it proved itself" \
    'DUMP
tag sha256:old rapido-go-panel:pre-update
PULL
up -d
HEALTH
image prune -f' \
    "$UPDATE_STUBS"'; cmd_update >/dev/null 2>&1; events'
unit "a good update rotates the backups" '5' \
    "$UPDATE_STUBS"'; rm -f "$SRC"; for i in 1 2 3 4 5 6 7; do : > "$DATA_DIR/backup-2020010$i-000000.sql.gz"; done
     cmd_update >/dev/null 2>&1; ls "$DATA_DIR"/backup-*.sql.gz | wc -l'
unit "a failed update rotates no backup and prunes no image" '7|0' \
    "$UPDATE_STUBS"'; for i in 1 2 3 4 5 6; do : > "$DATA_DIR/backup-2020010$i-000000.sql.gz"; done; PANEL_ANSWERS=0
     ( cmd_update ) >/dev/null 2>&1 || true
     printf "%s|%s" "$(ls "$DATA_DIR"/backup-*.sql.gz | wc -l)" "$(grep -c "image prune" "$LOG" || true)"'
unit "a failed update restores the pre-update dump along with the previous image" \
    'DUMP
tag sha256:old rapido-go-panel:pre-update
PULL
up -d
HEALTH
stop panel backend
DUMP
PSQL
FLUSH
tag sha256:old ghcr.io/x/rapido-go-panel:latest
up -d --remove-orphans
HEALTH' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; ( cmd_update ) >/dev/null 2>&1 || true; events'
unit_fails "a failed update exits non-zero even after a good rollback" \
    "$UPDATE_STUBS"'; compose() { case "$*" in "ps -a -q panel") echo cid ;; "up -d") return 1 ;; *) recorded_compose "$@" ;; esac; }; cmd_update'
unit "a failed migration counts as a failed update and restores the database" '1' \
    "$UPDATE_STUBS"'; compose() { case "$*" in "ps -a -q panel") echo cid ;; "up -d") return 1 ;; *) recorded_compose "$@" ;; esac; }
     ( cmd_update ) >/dev/null 2>&1 || true; grep -c psql "$LOG"'
unit "the rollback restores the dump taken before the update, not the one taken for the rollback" 'the old rows' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0
     compose() { case "$*" in
         "ps -a -q panel") echo cid ;;
         *pg_dump*) if [ -e "$DATA_DIR/seen" ]; then printf "%s\n" "the failed rows" "-- PostgreSQL database dump complete"; else : > "$DATA_DIR/seen"; printf "%s\n" "the old rows" "-- PostgreSQL database dump complete"; fi ;;
         *) recorded_compose "$@" ;; esac; }
     ( cmd_update ) >/dev/null 2>&1 || true; grep -o "the [a-z]* rows" "$STREAM"'
unit "the rollback puts back the files the update replaced" 'before' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; ( cmd_update ) >/dev/null 2>&1 || true; cat "$APP_DIR/docker-compose.prod.yml"'
unit "the database the failed update left behind is saved before the rollback" 'ok' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; ( cmd_update ) >/dev/null 2>&1 || true
     check_dump_file "$DATA_DIR"/pre-rollback-*.sql.gz && echo ok'
unit "a failed update with the same image leaves the database alone" '0|before' \
    "$UPDATE_STUBS"'; obtain_images() { :; }; PANEL_ANSWERS=0; ( cmd_update ) >/dev/null 2>&1 || true
     printf "%s|%s" "$(grep -c psql "$LOG" || true)" "$(cat "$APP_DIR/docker-compose.prod.yml")"'
unit_has "a failed update names the dump" '/backup-' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; ( cmd_update ) 2>&1 || true'
unit_has "a failed update names the previous image" 'rapido-go-panel:pre-update (sha256:old, was ghcr.io/x/rapido-go-panel:latest)' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; ( cmd_update ) 2>&1 || true'
unit_has "--no-rollback leaves a failed update in place and says how to go back" 'docker tag rapido-go-panel:pre-update ghcr.io/x/rapido-go-panel:latest' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; parse_update_args --no-rollback; ( cmd_update ) 2>&1 || true'
unit "--no-rollback touches nothing after the failure" 'DUMP
tag sha256:old rapido-go-panel:pre-update
PULL
up -d
HEALTH' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; parse_update_args --no-rollback; ( cmd_update ) >/dev/null 2>&1 || true; events'
unit_has "a rollback that fails says how to go back by hand" 'rapido-go restore ' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; COMPOSE_FAIL=psql; ( cmd_update ) 2>&1 || true'
unit "a rollback whose restore fails does not start the old image on the new schema" 'PSQL' \
    "$UPDATE_STUBS"'; PANEL_ANSWERS=0; COMPOSE_FAIL=psql; ( cmd_update ) >/dev/null 2>&1 || true; events | tail -n 1'
unit "with no panel container there is nothing to roll back to" 'DUMP
PULL
up -d
HEALTH' \
    "$UPDATE_STUBS"'; CONTAINER=""; PANEL_ANSWERS=0; ( cmd_update ) >/dev/null 2>&1 || true; events'
unit_fails "an image that cannot be kept stops the update before anything changes" \
    "$UPDATE_STUBS"'; docker() { case "$1" in tag) return 1 ;; inspect) echo sha256:old ;; esac; }; fetch_source() { echo UNEXPECTED_FETCH; }; cmd_update'
unit "update flags are parsed" '1|1' \
    'parse_update_args --no-rollback --replace-private-image; echo "$RAPIDO_NO_ROLLBACK|$RAPIDO_REPLACE_PRIVATE_IMAGE"'
unit_fails "an unknown update option is refused" 'parse_update_args --bogus'

# ---- a private build under the public name ----
PRIVATE_STUBS="$UPDATE_STUBS"'
     docker() {
         echo "docker $*" >> "$LOG"
         case "$*" in
             "inspect --format {{.Image}} cid") echo sha256:old ;;
             "image inspect --format {{index .Config.Labels \"org.rapido-go.build-tags\"}} sha256:old") echo "$RUNNING_TAGS" ;;
             "image inspect --format {{.Id}} "*) cat "$IMAGE_NOW" ;;
         esac
     }
     RUNNING_TAGS=private'
unit "a running private build refuses a pull under the public name" 'refused|0' \
    "$PRIVATE_STUBS"'; if ( pull_images ) >/dev/null 2>&1; then printf "pulled|"; else printf "refused|"; fi; grep -c "docker pull" "$LOG" || true'
unit_has "the refusal says what to do instead" '--replace-private-image' \
    "$PRIVATE_STUBS"'; ( pull_images ) 2>&1 || true'
unit "an update of a private build stops before it changes anything" 'refused|0|0' \
    "$PRIVATE_STUBS"'; fetch_source() { echo FETCH >> "$LOG"; }
     if ( cmd_update ) >/dev/null 2>&1; then printf "updated|"; else printf "refused|"; fi
     printf "%s|%s" "$(grep -c pg_dump "$LOG" || true)" "$(grep -c FETCH "$LOG" || true)"'
unit "the guard can be overridden on purpose" '1' \
    "$PRIVATE_STUBS"'; parse_update_args --replace-private-image; pull_images >/dev/null 2>&1; grep -c "docker pull -q ghcr.io/x/rapido-go-panel:latest" "$LOG"'
unit "a public build pulls as before" '1' \
    "$PRIVATE_STUBS"'; RUNNING_TAGS=""; pull_images >/dev/null 2>&1; grep -c "docker pull -q ghcr.io/x/rapido-go-panel:latest" "$LOG"'
unit "the alias keeps its own check and is not refused here" 'ok' \
    "$PRIVATE_STUBS"'; image_list() { echo rapido-go-panel:private; }; refuse_public_over_private && echo ok'
unit "a local build leaves the choice to build_local_image" 'ok' \
    "$PRIVATE_STUBS"'; RAPIDO_BUILD_LOCALLY=1; refuse_public_over_private && echo ok'
unit "a stopped private panel is still recognized" 'private' \
    "$PRIVATE_STUBS"'; running_panel_is_private && echo private'
unit_fails "a local build over a running private panel needs the add-on sources" \
    "$PRIVATE_STUBS"'; mkdir -p "$APP_DIR/docker"; : > "$APP_DIR/docker/Dockerfile.panel"; source_version() { echo test; }; build_local_image'

# ---- uninstall ----
UNINSTALL_STUBS="$DB_STUBS"'
     BIN_PATH="$(mktemp)"; mkdir -p "$APP_DIR/templates"; echo mine > "$APP_DIR/templates/clash.yml"; echo "POSTGRES_PASSWORD=x" > "$APP_DIR/.env"; : > "$APP_DIR/Caddyfile"'
unit "uninstall keeps the volumes unless asked" 'stop-only' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall <<<yes ) >/dev/null 2>&1
     if grep -q -- "--volumes" "$LOG"; then echo volumes-deleted; elif grep -q "down --remove-orphans" "$LOG"; then echo stop-only; fi'
unit_has "uninstall says the data stays" 'KEPT: the database, TLS certificates' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall <<<no ) 2>&1 || true'
unit "uninstall stops nothing without a yes" 'untouched' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall <<<no ) >/dev/null 2>&1 || true; [ -s "$LOG" ] || [ ! -d "$APP_DIR" ] || echo untouched'
unit "uninstall saves what the kept data cannot be used without" 'POSTGRES_PASSWORD=x|mine' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall <<<yes ) >/dev/null 2>&1; d="$(mktemp -d)"; tar xzf "$DATA_DIR"/config-*.tar.gz -C "$d"
     printf "%s|%s" "$(cat "$d/.env")" "$(cat "$d/templates/clash.yml")"'
unit "uninstall removes the files and the command" 'gone' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall <<<yes ) >/dev/null 2>&1; [ ! -e "$APP_DIR" ] && [ ! -e "$BIN_PATH" ] && echo gone'
unit "--purge does not take a plain yes" 'untouched' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall --purge <<<yes ) >/dev/null 2>&1 || true; [ -s "$LOG" ] || [ ! -d "$APP_DIR" ] || echo untouched'
unit_has "--purge says plainly what is lost" 'PERMANENTLY DELETES' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall --purge <<<no ) 2>&1 || true'
unit_has "--purge names the database and the certificates" 'the TLS certificates' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall --purge <<<no ) 2>&1 || true'
unit "--purge takes a final backup, then deletes the volumes" \
    'DUMP
down --remove-orphans --volumes' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall --purge <<<purge ) >/dev/null 2>&1; steps'
unit "--purge leaves a complete final dump" 'ok' \
    "$UNINSTALL_STUBS"'; ( cmd_uninstall --purge <<<purge ) >/dev/null 2>&1; check_dump_file "$DATA_DIR"/final-*.sql.gz && echo ok'
unit "no final backup means no purge" 'DUMP|kept' \
    "$UNINSTALL_STUBS"'; COMPOSE_FAIL=pg_dump; ( cmd_uninstall --purge <<<purge ) >/dev/null 2>&1 || true
     printf "%s|" "$(steps)"; [ -d "$APP_DIR" ] && echo kept'
unit "volumes that will not go keep the rest in place" 'kept' \
    "$UNINSTALL_STUBS"'; COMPOSE_FAIL=down; ( cmd_uninstall --purge <<<purge ) >/dev/null 2>&1 || true; [ -d "$APP_DIR" ] && echo kept'
unit_fails "a mistyped uninstall option is refused rather than ignored" \
    "$UNINSTALL_STUBS"'; cmd_uninstall --purg <<<yes'

# ---- secrets stay out of the process list ----
unit "the repository URL carries no token" 'https://github.com/rapidodev/rapido-go.git' \
    'RAPIDO_REPO_TOKEN=ghp_Secret123; repo_url'
GIT_STUB='git() { echo "ARGS:$*"; echo "ENV:${RAPIDO_GIT_TOKEN:-}|${GIT_TERMINAL_PROMPT:-}"; }; RAPIDO_REPO_TOKEN=ghp_Secret123'
unit_lacks "git never gets the token as an argument" 'ghp_Secret123' "$GIT_STUB"'; git_remote fetch origin | grep "^ARGS:"'
unit_has "git gets the token from the environment, with prompts off" 'ENV:ghp_Secret123|0' "$GIT_STUB"'; git_remote fetch origin'
unit_lacks "updating a checkout puts no token in any git call" 'ghp_Secret123' \
    'APP_DIR="$(mktemp -d)"; mkdir "$APP_DIR/.git"; RAPIDO_REPO_TOKEN=ghp_Secret123; git() { echo "$*" >&2; }; fetch_source_git'
unit_lacks "a fresh clone puts no token in any git call" 'ghp_Secret123' \
    'APP_DIR="$(mktemp -d)/new"; RAPIDO_REPO_TOKEN=ghp_Secret123; git() { echo "$*" >&2; }; fetch_source_git'
unit_has "a checkout is fetched at all" 'fetch --depth 1 origin' \
    'APP_DIR="$(mktemp -d)"; mkdir "$APP_DIR/.git"; RAPIDO_REPO_TOKEN=ghp_Secret123; git() { echo "$*" >&2; }; fetch_source_git'
if command -v git >/dev/null 2>&1; then
    unit_has "git really asks the credential helper for the token" 'password=ghp_Secret123' \
        'RAPIDO_REPO_TOKEN=ghp_Secret123; printf "protocol=https\nhost=github.com\n\n" | git_remote credential fill'
    unit_has "git gets nothing from the helper without a token" 'terminal prompts disabled' \
        'RAPIDO_REPO_TOKEN=""; printf "protocol=https\nhost=github.com\n\n" | LC_ALL=C git_remote credential fill 2>&1 || true'
else
    echo "  skip rapido-go.sh - credential helper (git is not installed)"
fi
if grep -nE '^ *(command|entrypoint):.*(postgres://|POSTGRES_PASSWORD)' docker-compose.prod.yml >/dev/null; then
    echo "  FAIL docker-compose.prod.yml - a database password is in a process's arguments"
    fail=1
else
    echo "  ok   docker-compose.prod.yml - no database password in a process's arguments"
fi

# ---- production compose file ----
if [ -n "$PY" ] && "$PY" -c 'import yaml' >/dev/null 2>&1; then
    compose_out="$("$PY" -c '
import sys, yaml
c = yaml.safe_load(open("docker-compose.prod.yml", encoding="utf-8"))
s = c["services"]
for name, svc in s.items():
    log = svc.get("logging") or {}
    opts = log.get("options") or {}
    if log.get("driver") != "json-file" or "max-size" not in opts or "max-file" not in opts:
        print("no log rotation:", name)
limited = sorted(n for n, svc in s.items() if "mem_limit" in svc)
if limited != ["caddy", "redis"]:
    print("memory limits on:", limited)
migrate = s["migrate"]
if migrate["command"] != ["up"] or not str(migrate["environment"]["GOOSE_DBSTRING"]).startswith("postgres://"):
    print("goose is not configured from the environment")
for name in ("postgres_data", "db_backups", "caddy_data", "ssh_data"):
    if name not in c["volumes"]:
        print("volume is gone:", name)
' 2>&1)" || true
    if [ -z "$compose_out" ]; then
        echo "  ok   docker-compose.prod.yml - log rotation everywhere, memory limits only on redis and caddy"
    else
        echo "  FAIL docker-compose.prod.yml - $compose_out"
        fail=1
    fi
else
    echo "  skip docker-compose.prod.yml - structure (no python with PyYAML)"
fi

# ---- migrating from the old panel ----
export MIGRATE_SRC; MIGRATE_SRC="$(prepare migrate-from-rapido.sh)"
# run_migrate <script args as one string> <code>: sources the script with those
# arguments and with what it looks for stubbed, so only its checks and helpers
# run. A check that stops the script leaves no output at all.
run_migrate() {
    bash -c 'set -euo pipefail; docker() { :; }; python3() { :; }; curl() { :; }; code="$2"; eval "set -- $1"; . "$MIGRATE_SRC" "$@" >/dev/null 2>&1; eval "$code"' \
        _ "$1" "$2" 2>&1 </dev/null
}
migrate_case() {  # migrate_case <label> <expected> <args> <code>
    local out
    out="$(run_migrate "$3" "$4")" || true
    if [ "$out" = "$2" ]; then
        echo "  ok   migrate-from-rapido.sh - $1"
    else
        echo "  FAIL migrate-from-rapido.sh - $1"
        echo "       expected: $2"
        echo "       got:      $out"
        fail=1
    fi
}
if grep -nE 'curl[^|]* -[a-zA-Z]*k|--insecure' migrate-from-rapido.sh >/dev/null; then
    echo "  FAIL migrate-from-rapido.sh - curl is told not to verify TLS"
    fail=1
else
    echo "  ok   migrate-from-rapido.sh - curl verifies TLS"
fi
migrate_case "a plain http:// target is refused" '' '--target http://x.example.com --user a --pass b' 'echo UNEXPECTED'
out="$(bash -c 'docker() { :; }; python3() { :; }; curl() { :; }; . "$MIGRATE_SRC" --target http://x.example.com --user a --pass b' 2>&1)" || true
case "$out" in
    *"must be an https:// URL"*) echo "  ok   migrate-from-rapido.sh - the refusal says why" ;;
    *) echo "  FAIL migrate-from-rapido.sh - the refusal says why (got: $out)"; fail=1 ;;
esac
migrate_case "no terminal and no flag pushes nothing" 'stopped' '--target https://x.example.com --user a --pass b' \
    '( confirm_replace ) </dev/null >/dev/null 2>&1 || echo stopped'
migrate_case "unattended replacing needs its own flag" 'go' '--target https://x.example.com --user a --pass b --replace-target-data' \
    'confirm_replace </dev/null; echo go'
migrate_case "a dry run needs no credentials" 'parsed' '--target https://x.example.com --dry-run' 'echo parsed'
migrate_case "no password and no terminal is refused" '' '--target https://x.example.com --user a' 'echo UNEXPECTED'
migrate_case "the password goes to curl on stdin, not as an argument" 'ARGS:-sS -X POST https://x.example.com/api/admin/token --data-urlencode username=a --data-urlencode password@-|STDIN:S3cret' \
    '--target https://x.example.com --user a --pass S3cret' \
    'curl() { echo "ARGS:$*|STDIN:$(cat)" >&2; }; python3() { cat >/dev/null; echo tok; }; login 2>&1 >/dev/null | grep "^ARGS:"'
migrate_case "the token goes to curl on stdin, not as an argument" 'ARGS:-sS -K - -X GET https://x.example.com/api/system|header = "Authorization: Bearer tok.en-1"' \
    '--target https://x.example.com --user a --pass b' \
    'curl() { echo "ARGS:$*|$(cat)"; }; TOKEN=tok.en-1; api GET /api/system'
migrate_case "a token cannot add to the curl config" 'tokurl=http//evil' \
    '--target https://x.example.com --user a --pass b' \
    'curl() { cat >/dev/null; }; python3() { cat >/dev/null; printf "tok\"\nurl=\"http://evil"; }; login >/dev/null 2>&1; printf "%s" "$TOKEN"'

# ---- node installer ----
# The served script, run for real under bash with stand-ins for what a test
# machine lacks: root, systemd, a panel and the node program.
NODE_FAKES="$WORK/node-fakes"; mkdir -p "$NODE_FAKES"
printf '#!/bin/sh\necho 0\n' > "$NODE_FAKES/id"
printf '#!/bin/sh\nexit 0\n' > "$NODE_FAKES/systemctl"
printf '#!/bin/sh\ncase "$1" in -m) echo x86_64;; *) echo Linux;; esac\n' > "$NODE_FAKES/uname"
cat > "$NODE_FAKES/curl" <<'FAKE'
#!/bin/sh
cat >/dev/null
while [ $# -gt 0 ]; do
    case "$1" in
        -o) dest="$2"; shift 2 ;;
        *) url="$1"; shift ;;
    esac
done
echo "$url" >> "$NODE_RECORD.curl"
case "$url" in
    *.sha256) sha256sum "$NODE_FAKES/node" > "$dest" ;;
    *) cp "$NODE_FAKES/node" "$dest" ;;
esac
printf 200
FAKE
cat > "$NODE_FAKES/node" <<'FAKE'
#!/bin/sh
[ "$1" = version ] && exit 0
printf '%s\n' "$@" > "$NODE_RECORD"
case " $* " in *" --setup-blob-file - "*) cat > "$NODE_RECORD.stdin" ;; esac
exit 0
FAKE
chmod +x "$NODE_FAKES"/*
NODE_BLOB="$(printf '{"cert":"c","key":"k","ca":"ca","secret":"0123456789abcdef","panel_url":"https://panel.example.com"}' | base64 | tr -d '\n')"

# run_node_installer <origin> <args...>: leaves its output in $NODE_OUT and what
# the node program was given in $NODE_RECORD.
run_node_installer() {
    local origin="$1" script; shift
    NODE_RECORD="$(mktemp -p "$WORK")"; : > "$NODE_RECORD"; rm -f "$NODE_RECORD.curl" "$NODE_RECORD.stdin"
    script="$(tr -d '\r' < internal/httpapi/templates/node-install.sh | sed "s#{{\.Origin}}#$origin#g")"
    NODE_OUT="$(printf '%s\n' "$script" | PATH="$NODE_FAKES:$PATH" NODE_FAKES="$NODE_FAKES" NODE_RECORD="$NODE_RECORD" bash -s -- "$@" 2>&1)" && NODE_RC=0 || NODE_RC=$?
}
node_case() {  # node_case <label> <condition, evaluated here>
    if eval "$2"; then
        echo "  ok   node-install.sh - $1"
    else
        echo "  FAIL node-install.sh - $1 (exit $NODE_RC)"
        printf '%s\n' "$NODE_OUT" | sed 's/^/       | /'
        printf 'recorded: %s\n' "$(tr '\n' ' ' < "$NODE_RECORD")" | sed 's/^/       | /'
        fail=1
    fi
}
if command -v sha256sum >/dev/null 2>&1 && command -v base64 >/dev/null 2>&1; then
    NODE_FILE="$WORK/node.blob"; printf '%s\n' "$NODE_BLOB" > "$NODE_FILE"; chmod 600 "$NODE_FILE"

    run_node_installer https://panel.example.com "$NODE_BLOB" --no-tune
    node_case "the older command still works, unchanged for the node program" \
        '[ "$NODE_RC" -eq 0 ] && [ "$(tr "\n" " " < "$NODE_RECORD")" = "install --setup-blob $NODE_BLOB --panel-url https://panel.example.com --no-tune " ]'

    if [ "$CHMOD_OK" = "1" ]; then
        run_node_installer https://panel.example.com --setup-blob-file "$NODE_FILE" --no-tune
        node_case "a blob from a file reaches the node program on stdin" \
            '[ "$NODE_RC" -eq 0 ] && [ "$(cat "$NODE_RECORD.stdin")" = "$NODE_BLOB" ]'
        node_case "a blob from a file is not in the node program's arguments" \
            '[ "$(tr "\n" " " < "$NODE_RECORD")" = "install --setup-blob-file - --panel-url https://panel.example.com --no-tune " ]'
        node_case "the installer says the file can go now" \
            'case "$NODE_OUT" in *"$NODE_FILE can be deleted"*) true ;; *) false ;; esac'

        chmod 644 "$NODE_FILE"
        run_node_installer https://panel.example.com --setup-blob-file "$NODE_FILE"
        node_case "a blob file other users can read is refused, before any download" \
            '[ "$NODE_RC" -ne 0 ] && [ ! -e "$NODE_RECORD.curl" ] && case "$NODE_OUT" in *"chmod 600"*) true ;; *) false ;; esac'
        chmod 600 "$NODE_FILE"

        run_node_installer https://panel.example.com "$NODE_BLOB" --setup-blob-file "$NODE_FILE"
        node_case "two blobs at once are refused" '[ "$NODE_RC" -ne 0 ] && [ ! -e "$NODE_RECORD.curl" ]'
    else
        echo "  skip node-install.sh - blob file (this filesystem ignores chmod)"
    fi

    run_node_installer https://panel.example.com
    node_case "with no blob and no terminal it stops and says how" \
        '[ "$NODE_RC" -ne 0 ] && [ ! -e "$NODE_RECORD.curl" ] && case "$NODE_OUT" in *"--setup-blob-file"*) true ;; *) false ;; esac'

    run_node_installer http://panel.example.com "$NODE_BLOB"
    node_case "a plain HTTP panel is refused before any download" \
        '[ "$NODE_RC" -ne 0 ] && [ ! -e "$NODE_RECORD.curl" ] && case "$NODE_OUT" in *"--allow-insecure-http"*) true ;; *) false ;; esac'

    run_node_installer http://127.0.0.1.example.com "$NODE_BLOB"
    node_case "a name that only looks like loopback is refused" '[ "$NODE_RC" -ne 0 ] && [ ! -e "$NODE_RECORD.curl" ]'

    run_node_installer http://panel.example.com "$NODE_BLOB" --allow-insecure-http
    node_case "the explicit flag allows the download, with a warning" \
        '[ "$NODE_RC" -eq 0 ] && [ -e "$NODE_RECORD.curl" ] && case "$NODE_OUT" in *"over plain HTTP"*) true ;; *) false ;; esac'
    node_case "that flag is the installer's own, the node program does not know it" \
        'case "$(cat "$NODE_RECORD")" in *allow-insecure*) false ;; *) true ;; esac'

    for origin in http://127.0.0.1:8000 http://localhost:8000 'http://[::1]:8000'; do
        run_node_installer "$origin" "$NODE_BLOB"
        node_case "$origin needs no flag: it never leaves this machine" '[ "$NODE_RC" -eq 0 ]'
    done
else
    echo "  skip node-install.sh - needs sha256sum and base64"
fi

# ---- help screen ----
if [ -n "$PY" ]; then
    width_out="$(run_unit 'usage' | PYTHONIOENCODING=utf-8 "$PY" -c '
import io, sys
data = io.TextIOWrapper(sys.stdin.buffer, encoding="utf-8")
widths = {len(l.rstrip("\r\n")) for l in data if l.startswith(("│", "╭", "├", "╰"))}
print("aligned" if len(widths) == 1 else "ragged: %s" % sorted(widths))')" || true
    if [ "$width_out" = "aligned" ]; then
        echo "  ok   rapido-go.sh - the help box lines are all the same width"
    else
        echo "  FAIL rapido-go.sh - the help box lines are all the same width ($width_out)"
        fail=1
    fi
else
    echo "  skip rapido-go.sh - help box alignment (no working python)"
fi
unit_has "help lists doctor" 'doctor' 'usage'

# ---- files that are run on Linux must be LF ----
for f in rapido-go.sh migrate-from-rapido.sh scripts/installer_test.sh docker-compose.prod.yml Caddyfile.example; do
    [ -f "$f" ] || continue
    if [ "$(tr -cd '\r' < "$f" | wc -c)" -eq 0 ]; then
        echo "  ok   $f - LF line endings"
    else
        echo "  FAIL $f - contains CR characters (CRLF breaks it on Linux)"
        fail=1
    fi
done

exit $fail
