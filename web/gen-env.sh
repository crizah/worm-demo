#!/usr/bin/env bash
# plain static site, no bundler - so there's no process.env to inject
# values into. this is the one manual step standing in for that: reads
# .env, writes env.js (a plain <script> the page loads before app.js).
# re-run this after editing .env.
set -euo pipefail
cd "$(dirname "$0")"

if [ ! -f .env ]; then
    echo ".env not found - copy .env.example to .env first" >&2
    exit 1
fi

{
    echo "// generated from .env by gen-env.sh - do not edit directly, and don't commit this file"
    echo "window.ENV = {"
    while IFS='=' read -r key value; do
        [ -z "$key" ] && continue
        case "$key" in
            \#*) continue ;;
        esac
        echo "  $key: \"$value\","
    done < .env
    echo "};"
} > env.js

echo "wrote env.js"
