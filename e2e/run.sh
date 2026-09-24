#!/usr/bin/env bash
# e2e/run.sh — offline e2e contract gate for nitter-cli.
#
# Exit-code contract:
#   0 — every check passed (skips allowed)
#   1 — at least one check failed
#   2 — nothing ran except skips (soft pass for schedulers)
#
# Segments:
#   - Offline contracts (always run, no network): --version line, fresh-HOME
#     config publication (0600 where stat can see POSIX modes), help exit
#     codes, usage-error exit codes, and nitter.pipeline/v1 NDJSON envelope
#     validation against an unreachable instance (connection refused is
#     deterministic and offline).
#   - Live read-only probes (env-gated): only when BOTH NITTER_CLI_E2E_INSTANCE
#     and NITTER_CLI_E2E_USER are set; otherwise they count as skips.
#
# All runs are sandboxed: HOME/USERPROFILE point at a throwaway directory, so
# the real ~/.nitter-cli is never touched. Invoked as `bash e2e/run.sh`.
set -u

cd "$(dirname "$0")/.."

pass=0
fail=0
skip=0

pass() { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
fail() { fail=$((fail + 1)); printf 'FAIL - %s: %s\n' "$1" "$2"; }
skip() { skip=$((skip + 1)); printf 'skip - %s (%s)\n' "$1" "$2"; }

# check <label> <want_exit> <pattern> -- cmd...
#   Runs cmd, asserts its exit code and (when <pattern> is non-empty) that the
#   combined stdout+stderr matches the grep -E pattern.
check() {
	local label="$1" want="$2" pattern="$3"
	shift 3
	[ "${1:-}" = "--" ] && shift
	local out="$SANDBOX/check.out" err="$SANDBOX/check.err" rc
	"$@" >"$out" 2>"$err"
	rc=$?
	if [ "$rc" -ne "$want" ]; then
		fail "$label" "exit $rc, want $want; stderr: $(tail -c 300 "$err" | tr '\n' ' ')"
		return
	fi
	if [ -n "$pattern" ] && ! cat "$out" "$err" | grep -Eq "$pattern"; then
		fail "$label" "output does not match /$pattern/"
		return
	fi
	pass "$label"
}

# Resolve a working python interpreter (python3 preferred). The Windows Store
# python3 stub exists on PATH but fails to run, hence the smoke test.
PY=""
for cand in python3 python; do
	if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import json,sys' >/dev/null 2>&1; then
		PY="$cand"
		break
	fi
done

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
export HOME="$SANDBOX/home"
export USERPROFILE="$SANDBOX/home" # os.UserHomeDir reads USERPROFILE on Windows
mkdir -p "$HOME"

# Build the binary if it is not there yet (CI runs scripts/build.sh first).
if [ ! -x ./nitter ]; then
	echo "building ./nitter (missing) via scripts/build.sh"
	sh scripts/build.sh || {
		echo "FAIL - build: scripts/build.sh failed"
		exit 1
	}
fi

echo "== offline contracts =="

# --version must print the stable version line, and a version probe must NOT
# publish the baseline config (only real commands do).
check "--version prints version line" 0 '^nitter version' -- ./nitter --version
if [ -f "$HOME/.nitter-cli/config.toml" ]; then
	fail "--version does not publish config" "config.toml exists after --version"
else
	pass "--version does not publish config"
fi

# config path prints the app dir and the first real command publishes the
# baseline config in the fresh HOME.
check "config path prints app dir" 0 '\.nitter-cli' -- ./nitter config path
if [ -f "$HOME/.nitter-cli/config.toml" ]; then
	pass "config path publishes baseline config"
else
	fail "config path publishes baseline config" "config.toml missing after config path"
fi
case "$(uname -s)" in
Linux | Darwin)
	mode="$(stat -c '%a' "$HOME/.nitter-cli/config.toml")"
	if [ "$mode" = "600" ]; then
		pass "config file mode 0600"
	else
		fail "config file mode 0600" "mode is $mode"
	fi
	;;
*)
	skip "config file mode 0600" "POSIX stat modes not meaningful on $(uname -s)"
	;;
esac

check "user --help exits 0" 0 'Usage' -- ./nitter user --help
check "watch without sources exits 2" 2 '' -- ./nitter watch --once --ndjson
check "watch --json without --once exits 2" 2 '' -- ./nitter watch user:e2e --json
# The caps have no "unlimited" value: 0 and negatives are usage errors, and the
# rejection must happen before any network (no fixture is set up here).
check "user --limit 0 exits 2" 2 'must be >= 1' -- ./nitter user e2e --limit 0
check "user --max-pages 0 exits 2" 2 'must be >= 1' -- ./nitter user e2e --max-pages 0
check "watch --max-pages 0 exits 2" 2 'must be >= 1' -- ./nitter watch user:e2e --once --max-pages 0
check "seen list on empty store" 0 '\(empty\)' -- ./nitter seen list
check "seen clear without --confirm exits 2" 2 '' -- ./nitter seen clear

# NDJSON error envelope: a config fixture pointing at an unreachable instance
# (port 1 on loopback — connection refused, no network) must produce a
# well-formed nitter.pipeline/v1 error envelope and exit 1 (--once with a
# failed source).
cat >"$HOME/.nitter-cli/config.toml" <<'TOML'
default_limit     = 20
max_pages         = 5
request_interval  = "0s"
retry_attempts    = 2
retry_delay       = "0s"
instance_cooldown = "0s"
proxy             = ""
log_level         = "info"
log_format        = "text"

[[instances]]
url = "http://127.0.0.1:1"
TOML
./nitter watch user:e2eoffline --once --ndjson --state-dir "$SANDBOX/watch-state" \
	>"$SANDBOX/watch.ndjson" 2>"$SANDBOX/watch.err"
rc=$?
if [ "$rc" -ne 1 ]; then
	fail "watch offline error envelope" "exit $rc, want 1; stderr: $(tail -c 300 "$SANDBOX/watch.err" | tr '\n' ' ')"
elif [ -z "$PY" ]; then
	skip "watch offline error envelope" "no python interpreter for JSON validation"
else
	if "$PY" - "$SANDBOX/watch.ndjson" <<'PYEOF'
import json, sys
lines = [l for l in open(sys.argv[1], encoding="utf-8") if l.strip()]
assert lines, "no NDJSON records on stdout"
for line in lines:
    e = json.loads(line)
    assert e.get("schema") == "nitter.pipeline/v1", e
    assert e.get("kind") == "error", e
    d = e["data"]
    assert d["command"] == "watch", d
    assert d["stage"], d
    assert d["code"], d
    assert d["message"], d
    assert e["meta"]["input"] == "user:e2eoffline", e
PYEOF
	then
		pass "watch offline error envelope"
	else
		fail "watch offline error envelope" "envelope schema validation failed"
	fi
fi

# --once --json document: the same unreachable-instance fetch must produce
# ONE JSON document — an empty tweets array plus one {ref, code, message}
# entry for the failed source — and exit 1 (the failed-source summary is
# unchanged).
./nitter watch user:e2eoffline --once --json --state-dir "$SANDBOX/watch-state-json" \
	>"$SANDBOX/watch.json" 2>"$SANDBOX/watch-json.err"
rc=$?
if [ "$rc" -ne 1 ]; then
	fail "watch --once --json document" "exit $rc, want 1; stderr: $(tail -c 300 "$SANDBOX/watch-json.err" | tr '\n' ' ')"
elif [ -z "$PY" ]; then
	skip "watch --once --json document" "no python interpreter for JSON validation"
else
	if "$PY" - "$SANDBOX/watch.json" <<'PYEOF'
import json, sys
doc = json.loads(open(sys.argv[1], encoding="utf-8").read())
assert set(doc) == {"tweets", "errors"}, doc
assert doc["tweets"] == [], doc["tweets"]
errs = doc["errors"]
assert len(errs) == 1, errs
e = errs[0]
assert e["ref"] == "user:e2eoffline", e
assert e["code"], e
assert e["message"], e
PYEOF
	then
		pass "watch --once --json document"
	else
		fail "watch --once --json document" "document schema validation failed"
	fi
fi

# instances test against the same unreachable instance: the report is the
# product (exit 0). Since 0.6.0 the piped default is NDJSON — stdout is a file
# here (no --ndjson flag given), so the stream must carry an
# nitter.pipeline/v1 instance_report envelope whose RSS probe failed.
./nitter instances test http://127.0.0.1:1 \
	>"$SANDBOX/instances.ndjson" 2>"$SANDBOX/instances.err"
rc=$?
if [ "$rc" -ne 0 ]; then
	fail "instances test offline piped-default envelope" "exit $rc, want 0; stderr: $(tail -c 300 "$SANDBOX/instances.err" | tr '\n' ' ')"
elif [ -z "$PY" ]; then
	skip "instances test offline piped-default envelope" "no python interpreter for JSON validation"
else
	if "$PY" - "$SANDBOX/instances.ndjson" <<'PYEOF'
import json, sys
lines = [l for l in open(sys.argv[1], encoding="utf-8") if l.strip()]
assert lines, "no NDJSON records on stdout"
for line in lines:
    e = json.loads(line)
    assert e.get("schema") == "nitter.pipeline/v1", e
    assert e.get("kind") == "instance_report", e
    assert e.get("id") == "http://127.0.0.1:1", e
    assert e["data"]["rss"]["ok"] is False, e
PYEOF
	then
		pass "instances test offline piped-default envelope"
	else
		fail "instances test offline piped-default envelope" "envelope schema validation failed"
	fi
fi

echo "== live read-only probes (env-gated) =="

if [ -n "${NITTER_CLI_E2E_INSTANCE:-}" ] && [ -n "${NITTER_CLI_E2E_USER:-}" ]; then
	cat >"$HOME/.nitter-cli/config.toml" <<TOML
default_limit     = 5
max_pages         = 1
request_interval  = "0s"
retry_attempts    = 2
retry_delay       = "0s"
instance_cooldown = "0s"
proxy             = ""
log_level         = "info"
log_format        = "text"

[[instances]]
url = "$NITTER_CLI_E2E_INSTANCE"
TOML

	check "live user --json" 0 '' -- ./nitter user "$NITTER_CLI_E2E_USER" --limit 5 --json
	if [ -z "$PY" ]; then
		skip "live user --json field assertions" "no python interpreter"
	else
		if "$PY" - "$SANDBOX/check.out" "$SANDBOX/live.id" <<'PYEOF'
import json, sys
raw = open(sys.argv[1], encoding="utf-8").read()
records = json.loads(raw)
if isinstance(records, dict):
    records = [records]
assert records, "user --json returned no tweets"
t = records[0]
for key in ("id", "url", "text", "author", "published_at", "media", "is_retweet"):
    assert key in t, key
assert t["id"] and t["author"]["handle"], t
open(sys.argv[2], "w", encoding="utf-8").write(t["id"])
PYEOF
		then
			pass "live user --json field assertions"
		else
			fail "live user --json field assertions" "tweet shape incomplete"
		fi
	fi

	check "live search --json" 0 '' -- ./nitter search "from:$NITTER_CLI_E2E_USER" --limit 5 --json
	if [ -z "$PY" ]; then
		skip "live search --json field assertions" "no python interpreter"
	elif "$PY" - "$SANDBOX/check.out" <<'PYEOF'
import json, sys
records = json.loads(open(sys.argv[1], encoding="utf-8").read())
if isinstance(records, dict):
    records = [records]
assert records, "search --json returned no tweets"
t = records[0]
for key in ("id", "url", "text", "author", "published_at", "media", "is_retweet"):
    assert key in t, key
PYEOF
	then
		pass "live search --json field assertions"
	else
		fail "live search --json field assertions" "tweet shape incomplete"
	fi

	if [ -f "$SANDBOX/live.id" ] && [ -s "$SANDBOX/live.id" ]; then
		status_id="$(cat "$SANDBOX/live.id")"
		check "live get --json" 0 '' -- ./nitter get "$status_id" --json
		if [ -z "$PY" ]; then
			skip "live get --json field assertions" "no python interpreter"
		elif "$PY" - "$SANDBOX/check.out" "$status_id" <<'PYEOF'
import json, sys
t = json.loads(open(sys.argv[1], encoding="utf-8").read())
assert isinstance(t, dict), type(t)
assert t["id"] == sys.argv[2], t["id"]
for key in ("url", "text", "author", "published_at", "media", "is_retweet"):
    assert key in t, key
PYEOF
		then
			pass "live get --json field assertions"
		else
			fail "live get --json field assertions" "status shape incomplete"
		fi
	else
		skip "live get --json" "no status id captured from the user probe"
	fi
else
	skip "live user/search/get probes" "NITTER_CLI_E2E_INSTANCE / NITTER_CLI_E2E_USER not set"
fi

echo "summary: pass=$pass fail=$fail skip=$skip"
[ "$fail" -gt 0 ] && exit 1
[ "$pass" -eq 0 ] && [ "$skip" -gt 0 ] && exit 2
exit 0
