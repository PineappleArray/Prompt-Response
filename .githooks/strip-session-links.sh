#!/usr/bin/env bash
# Strip claude.ai session links from the most recent commit message.
# Runs as a PostToolUse hook after Bash commands and as a git commit-msg hook.
#
# As commit-msg hook: receives the commit message file path as $1.
# As PostToolUse hook: amends the last commit if it contains a session link.

set -euo pipefail

strip_pattern='https://claude\.ai/code/session_[A-Za-z0-9_-]*'

# Mode 1: git commit-msg hook (called with message file path)
if [ -n "${1:-}" ] && [ -f "${1:-}" ]; then
    sed -i -E "s|${strip_pattern}||g" "$1"
    # Remove blank lines left behind at end of message
    sed -i -e :a -e '/^\n*$/{$d;N;ba' -e '}' "$1"
    exit 0
fi

# Mode 2: PostToolUse hook (amend last commit if needed)
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    msg=$(git log -1 --format=%B 2>/dev/null || true)
    if echo "$msg" | grep -qE "$strip_pattern"; then
        cleaned=$(echo "$msg" | sed -E "s|${strip_pattern}||g" | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')
        git commit --amend -m "$cleaned" --no-verify >/dev/null 2>&1 || true
    fi
fi
