#!/usr/bin/env bash
#
# Delete every installer asset on a release except the one that is about to be
# published.
#
# This must run BEFORE the upload. The runtime updater's pickInstallerAsset
# refuses a release that carries more than one installer asset ("refusing an
# ambiguous update"), so a stale asset left in place does not merely waste
# space -- it freezes every client on that channel until a later run clears it.
#
# Usage: prune-installer-assets.sh TAG EXPECTED_ASSET
# Requires GH_TOKEN and GITHUB_REPOSITORY in the environment.

set -uo pipefail

tag=${1:?usage: prune-installer-assets.sh TAG EXPECTED_ASSET}
expected=${2:?usage: prune-installer-assets.sh TAG EXPECTED_ASSET}
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"

attempts=5

# Retry transient GitHub API failures. An unretried 503 from delete-asset has
# already turned an otherwise-healthy scheduled run red.
#
# stdout is buffered and replayed only after a successful attempt, because gh
# writes API error bodies to stdout. Streaming it would let a caller's $(...)
# capture a 404/503 body as data -- the bug that once put a JSON blob into a
# release-asset URL. gh's stderr is left alone so the real error stays in the log.
retry() {
  local attempt status buffer
  buffer=$(mktemp)
  for attempt in $(seq 1 "$attempts"); do
    "$@" >"$buffer"
    status=$?
    if [ "$status" -eq 0 ]; then
      cat "$buffer"
      rm -f "$buffer"
      return 0
    fi
    if [ "$attempt" -lt "$attempts" ]; then
      echo "Attempt $attempt/$attempts of '$*' failed (exit $status); retrying in $((2 ** attempt))s." >&2
      sleep $((2 ** attempt))
    fi
  done
  rm -f "$buffer"
  echo "Gave up on '$*' after $attempts attempts." >&2
  return 1
}

# Mirror of isInstallerAsset in update.go, which is what actually decides
# whether a release looks ambiguous to the updater:
#
#   regexp.MustCompile(`(?i)^CodexLB_Installer_[0-9A-Za-z][0-9A-Za-z._-]*\.exe$`)
#
# The (?i) matters. A bash `case` glob is case-sensitive, so an asset named
# ...EXE would be invisible to the prune while still counting toward the
# updater's one-installer limit -- the release would look clean here, gain a
# second asset on upload, and freeze the channel behind a green build. Keep
# this predicate and the Go one in step.
is_installer_asset() {
  printf '%s\n' "$1" | grep -qiE '^CodexLB_Installer_[0-9A-Za-z][0-9A-Za-z._-]*\.exe$'
}

# Print every installer asset on $tag that is not $expected, one per line.
# Returns non-zero only when the release could not be read at all, so callers
# can tell "nothing to prune" apart from "we do not know what is there".
list_stale() {
  local names name
  names=$(retry gh release view "$tag" --repo "$GITHUB_REPOSITORY" \
            --json assets --jq '.assets[].name') || return 1
  # tr -d '\r' because tooling on the Windows runner emits CRLF. Command
  # substitution strips only the TRAILING newline/CR, so mid-stream CRs would
  # otherwise survive into the loop.
  while read -r name; do
    [ -n "$name" ] || continue
    is_installer_asset "$name" || continue
    [ "$name" = "$expected" ] || printf '%s\n' "$name"
  done <<<"$(printf '%s\n' "$names" | tr -d '\r')"
  return 0
}

# Absence is only ever concluded from a listing that SUCCEEDED and did not
# contain the tag. The previous version treated any non-zero exit from
# `gh release view` as "no prior release exists", so a single 503 here would
# silently skip the prune and let the upload leave two installer assets on the
# release -- breaking updates for everyone on the channel, with a green build.
tags=$(retry gh release list --repo "$GITHUB_REPOSITORY" \
         --limit 1000 --json tagName --jq '.[].tagName') || {
  echo "Could not list releases for $GITHUB_REPOSITORY; refusing to guess that $tag is absent." >&2
  exit 1
}

if ! printf '%s\n' "$tags" | tr -d '\r' | grep -qxF "$tag"; then
  echo "No release tagged $tag exists yet; nothing to prune."
  exit 0
fi

stale=$(list_stale) || {
  echo "Could not read the assets on $tag; refusing to assume it is already clean." >&2
  exit 1
}
if [ -z "$stale" ]; then
  echo "$tag carries no installer asset other than $expected; nothing to prune."
  exit 0
fi

while read -r name; do
  [ -n "$name" ] || continue
  echo "Deleting superseded installer asset $name from $tag."
  # Best effort: the end state is verified below, and GitHub can return a 5xx
  # for a delete that actually took effect.
  retry gh release delete-asset "$tag" "$name" --repo "$GITHUB_REPOSITORY" --yes || true
done <<<"$stale"

# Verify the invariant the updater depends on, rather than trusting that each
# individual DELETE reported success.
stale=$(list_stale) || {
  echo "Could not re-read the assets on $tag to confirm the prune took effect." >&2
  exit 1
}
if [ -n "$stale" ]; then
  echo "Refusing to publish: $tag still carries installer assets other than $expected:" >&2
  printf '%s\n' "$stale" | sed 's/^/  /' >&2
  echo "Publishing now would leave the release ambiguous and the updater would reject it." >&2
  exit 1
fi

echo "Prune complete; $tag carries no installer asset other than $expected."
