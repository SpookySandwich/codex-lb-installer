#!/usr/bin/env bash
# Exercises .github/scripts/prune-installer-assets.sh against a stubbed gh.
set -uo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/prune-installer-assets.sh"
STUB=$(mktemp -d)
export GITHUB_REPOSITORY="Owner/repo"
export GH_TOKEN="x"

EXPECTED="CodexLB_Installer_edge_aaa_bbb.exe"
STALE="CodexLB_Installer_edge_old_zzz.exe"

# Fake sleep so retry backoff does not make the suite slow.
cat >"$STUB/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF

# Stub gh. Behaviour is driven by files in $STUB written per test case.
cat >"$STUB/gh" <<'EOF'
#!/usr/bin/env bash
sub="$1 $2"
case "$sub" in
  "release list")
    if [ -f "$STUB/list_fails" ]; then echo "gh: 503 body on stdout"; exit 1; fi
    cat "$STUB/tags"
    ;;
  "release view")
    if [ -f "$STUB/view_fails" ]; then echo "gh: 503 body on stdout"; exit 1; fi
    cat "$STUB/assets"
    ;;
  "release delete-asset")
    echo "$4" >>"$STUB/delete_calls"
    n=$(grep -c . "$STUB/delete_calls")
    fail_until=$(cat "$STUB/delete_fail_until" 2>/dev/null || echo 0)
    if [ "$n" -le "$fail_until" ]; then
      echo "HTTP 503: No server is currently available" >&2
      exit 1
    fi
    if [ -f "$STUB/delete_noop" ]; then exit 1; fi
    grep -vxF "$4" "$STUB/assets" >"$STUB/assets.tmp" || true
    mv "$STUB/assets.tmp" "$STUB/assets"
    # delete_lies: the delete took effect but the API still reports failure,
    # which is what a 5xx on a mutating call can look like.
    if [ -f "$STUB/delete_lies" ]; then exit 1; fi
    ;;
esac
exit 0
EOF
chmod +x "$STUB/gh" "$STUB/sleep"
export STUB
export PATH="$STUB:$PATH"

pass=0; fail=0
reset() {
  rm -f "$STUB"/list_fails "$STUB"/view_fails "$STUB"/delete_noop \
        "$STUB"/delete_calls "$STUB"/delete_fail_until "$STUB"/delete_lies
  : >"$STUB/delete_calls"
  printf 'edge\nv1.2.3\n' >"$STUB/tags"
  printf '%s\n' "$EXPECTED" >"$STUB/assets"
}
check() { # name expected_exit must_contain
  local name=$1 want=$2 needle=$3
  local out rc
  out=$(bash "$SCRIPT" edge "$EXPECTED" 2>&1); rc=$?
  if [ "$rc" -eq "$want" ] && printf '%s' "$out" | grep -qF "$needle"; then
    echo "  PASS  $name"; pass=$((pass+1))
  else
    echo "  FAIL  $name (exit $rc, wanted $want)"; printf '%s\n' "$out" | sed 's/^/        /'
    fail=$((fail+1))
  fi
}

echo "A. tag absent -> clean skip"
reset; printf 'v1.2.3\n' >"$STUB/tags"
check "absent tag exits 0" 0 "No release tagged edge exists yet"

echo "B. release list keeps failing -> must NOT assume absent (the latent bug)"
reset; touch "$STUB/list_fails"
check "list failure refuses to guess" 1 "refusing to guess"

echo "C. only the expected asset present -> no deletes"
reset
check "nothing to prune" 0 "nothing to prune"
[ ! -s "$STUB/delete_calls" ] && { echo "  PASS  issued no deletes"; pass=$((pass+1)); } \
  || { echo "  FAIL  issued deletes"; fail=$((fail+1)); }

echo "D. one stale asset -> deleted"
reset; printf '%s\n%s\n' "$EXPECTED" "$STALE" >"$STUB/assets"
check "prunes stale asset" 0 "Prune complete"
grep -qxF "$STALE" "$STUB/delete_calls" && { echo "  PASS  deleted the stale one"; pass=$((pass+1)); } \
  || { echo "  FAIL  wrong asset deleted"; fail=$((fail+1)); }

echo "E. delete 503s twice then succeeds -> retry saves the run (the observed failure)"
reset; printf '%s\n%s\n' "$EXPECTED" "$STALE" >"$STUB/assets"; echo 2 >"$STUB/delete_fail_until"
check "retries through 503" 0 "Prune complete"

echo "F. delete always reports failure but asset is gone -> end-state check passes"
reset; printf '%s\n%s\n' "$EXPECTED" "$STALE" >"$STUB/assets"; touch "$STUB/delete_lies"
check "trusts end state over per-call status" 0 "Prune complete"

echo "G. delete truly fails, asset remains -> refuse to publish"
reset; printf '%s\n%s\n' "$EXPECTED" "$STALE" >"$STUB/assets"; touch "$STUB/delete_noop"
check "refuses when still ambiguous" 1 "Refusing to publish"

echo "H. CRLF in gh output is tolerated"
# Asserts the end-to-end property: a CRLF-laden API response still gets the
# stale asset pruned. It deliberately puts the stale entry FIRST (command
# substitution strips only the trailing CR) and asserts "Prune complete"
# rather than "nothing to prune", which the tag-absent branch also prints.
#
# Honest limitation: this still cannot prove the `tr -d '\r'` calls are doing
# the work. Deleting both of them leaves this suite green, because Git Bash --
# the shell these jobs actually run -- reads in text mode and tolerates a
# trailing CR in grep and in `case`. The strippers are kept as defence in
# depth for other shells and tooling, not because this test pins them.
reset; printf 'edge\r\nv1.2.3\r\n' >"$STUB/tags"
printf '%s\r\n%s\r\n' "$STALE" "$EXPECTED" >"$STUB/assets"
check "handles CRLF" 0 "Prune complete"
grep -qxF "$STALE" "$STUB/delete_calls" \
  && { echo "  PASS  pruned the stale asset despite CRLF"; pass=$((pass+1)); } \
  || { echo "  FAIL  CR-laden stale asset was not pruned"; fail=$((fail+1)); }

echo "I. release view keeps failing -> must NOT assume nothing is stale"
reset; touch "$STUB/view_fails"
check "view failure refuses to guess" 1 "refusing to assume it is already clean"
[ ! -s "$STUB/delete_calls" ] && { echo "  PASS  issued no deletes"; pass=$((pass+1)); } \
  || { echo "  FAIL  deleted something despite an unreadable release"; fail=$((fail+1)); }

echo "J. differently-cased asset is pruned (updater's isInstallerAsset is (?i))"
reset; printf '%s\n%s\n' "$EXPECTED" "CodexLB_Installer_edge_old_zzz.EXE" >"$STUB/assets"
check "matches case-insensitively" 0 "Prune complete"
grep -qxF "CodexLB_Installer_edge_old_zzz.EXE" "$STUB/delete_calls" \
  && { echo "  PASS  pruned the .EXE variant"; pass=$((pass+1)); } \
  || { echo "  FAIL  .EXE variant left behind; updater would see two assets"; fail=$((fail+1)); }

echo
echo "passed=$pass failed=$fail"
rm -rf "$STUB"
[ "$fail" -eq 0 ]
