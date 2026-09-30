#!/usr/bin/env bash
# ExecStart target for worm-resume.service. Picks migrate-resume or
# migrate-data based on whether resume's stage claim actually succeeds -
# no state db inspection needed.
#
# First-ever run (or anything before migrate-data has ever completed):
# migrate-resume's claimStage([2,3], 3) fails outright and it exits
# non-zero -> falls through to migrate-data.
# Every run after that: migrate-resume succeeds and just runs (forever,
# until something stops or crashes it) - the fallback line is never
# reached again until it actually exits.
set -u
cd "$(dirname "$0")"

if ./worm migrate-resume; then
    exit 0
fi
exec ./worm migrate-data
