#!/bin/sh
# run.sh checks a livesim2 server with dashcheck (https://github.com/tobbee/dashcheck) and
# fails only on errors that are not listed in a known-failures file.
#
# Usage: run.sh <host> <profile> <known-failures file> [dashcheck options]
#
# The URLs come from urls.sh (see there for the profiles). The dashcheck JSON report is
# written to dashcheck-report.json in the current directory.
#
# Each line of the known-failures file is "<rule> <path regex>", where the regex (ERE) is
# matched against the URL without the host, e.g.
#
#   INIT-STRUCTURE /testpic_2s/(Manifest_stpp|cea608_and_segs)\.mpd  # S1 init: no trex for track 3
#
# The run fails on any error finding that no line matches. Lines that match nothing in
# this run are reported, so that the list shrinks as fixes land, but do not fail the run.
# A ~ before the rule marks an error that depends on when the check runs, such as an MPD
# update at an ad break, and such a line is not reported when it matches nothing.
#
# To check a local server against the test assets of this repository:
#
#   go build -o out/livesim2 ./cmd/livesim2
#   out/livesim2 --vodroot=cmd/livesim2/app/testdata/assets --drmcfgfile=pkg/drm/testdata/drm_config_test.json --c2pa &
#   DRM=EZDRM-1-key-cbcs-test scripts/dashcheck/run.sh http://localhost:8888 testdata scripts/dashcheck/known-failures-testdata.txt
set -eu

if [ $# -lt 3 ]; then
	echo "usage: $0 <host> testdata|production <known-failures file> [dashcheck options]" >&2
	exit 2
fi
H=${1%/}
PROFILE=$2
KNOWN=$3
shift 3
DIR=$(dirname "$0")
REPORT=dashcheck-report.json
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

"$DIR/urls.sh" "$H" "$PROFILE" > "$TMP/urls"
VERSION=$(dashcheck -version)
echo "Checking $(wc -l < "$TMP/urls" | tr -d ' ') MPDs on $H ($PROFILE) with $VERSION"

# dashcheck exits with 1 when it finds errors; the known failures decide the verdict instead.
xargs dashcheck -short -json -level error "$@" < "$TMP/urls" > "$REPORT" || true
checked=$(jq length "$REPORT")
if [ "$checked" -ne "$(wc -l < "$TMP/urls")" ]; then
	echo "dashcheck reported $checked MPDs, expected $(wc -l < "$TMP/urls")" >&2
	exit 1
fi

# One "<rule> <path> <message>" line per error, with the host removed from the URL.
jq -r --arg host "$H" '.[] | .mpdURL as $u | (.findings // [])[]
	| select(.severity == "error")
	| "\(.rule) \($u | ltrimstr($host)) \(.message | gsub("\n"; " "))"' "$REPORT" | sort -u > "$TMP/errors"

awk -v known="$KNOWN" -v unused="$TMP/unused" '
	BEGIN {
		while ((getline line < known) > 0) {
			sub(/#.*/, "", line)
			if (split(line, f, " ") < 2) continue
			n++; rule[n] = f[1]; re[n] = f[2]; src[n] = line
			if (sub(/^~/, "", rule[n])) intermittent[n] = 1
		}
	}
	{
		hit = 0
		for (i = 1; i <= n; i++) {
			if ($1 == rule[i] && $2 ~ ("^" re[i] "$")) { used[i] = 1; hit = 1 }
		}
		if (!hit) { print; bad++ }
	}
	END {
		for (i = 1; i <= n; i++) if (!used[i] && !intermittent[i]) print src[i] > unused
		exit (bad > 0)
	}' "$TMP/errors" > "$TMP/new" && verdict=0 || verdict=1

summary() {
	echo "### dashcheck $PROFILE: $H"
	echo
	echo "$VERSION"
	echo
	echo "$checked MPDs checked, $(wc -l < "$TMP/errors" | tr -d ' ') errors, $(wc -l < "$TMP/new" | tr -d ' ') of them not in $(basename "$KNOWN")."
	if [ -s "$TMP/new" ]; then
		echo
		echo "New errors:"
		echo '```'
		cat "$TMP/new"
		echo '```'
	fi
	if [ -s "$TMP/unused" ]; then
		echo
		echo "Known failures not seen in this run (remove them once they are fixed):"
		echo '```'
		cat "$TMP/unused"
		echo '```'
	fi
}
summary
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	summary >> "$GITHUB_STEP_SUMMARY"
fi
exit $verdict
