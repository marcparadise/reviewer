#!/bin/bash
# Wrap git commit with the required AI author/committer identity.
# Rule: author and committer must both be "AI Agent <agent@example.com>".
# Do NOT change git config — specify it explicitly via CLI.
#
# When the current repo is a reviewer project whose review is in
# changes_requested with outstanding comments, this script appends
# a "Review-Response" block to the commit message automatically. That response will contain the review feedback, so that it's associated withthe commit permanently.
#

set -e

if [[ $# -eq 0 ]]; then
    echo "Usage: $0 <git-commit-args...>"
    echo "Example: $0 -m \"summary line\" -m \"detail line one\" -m \"detail line 2\""
    echo ""
    echo "Wraps 'git commit' with author and committer set to AI Agent."
    echo "When the review is in changes_requested, appends review feedback."
    exit 1
fi

export GIT_COMMITTER_NAME="AI Agent"
export GIT_COMMITTER_EMAIL="agent@example.com"

# Save original args for fallback path.
original_args=("$@")

# Parse arguments into message parts and passthrough args.
msg_parts=()
passthrough=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        -m)
            msg_parts+=("$2")
            shift 2
            ;;
        -m*)
            msg_parts+=("${1#-m}")
            shift
            ;;
        --message)
            msg_parts+=("$2")
            shift 2
            ;;
        --message=*)
            msg_parts+=("${1#--message=}")
            shift
            ;;
        *)
            passthrough+=("$1")
            shift
            ;;
    esac
done

for part in "${msg_parts[@]}"; do
    if offender=$(grep -iE '^[[:space:]]*co-authored-by:' <<<"$part"); then
        echo "ERROR: commit message contains a Co-authored-by trailer:" >&2
        echo "  $offender" >&2
        echo "Remove it and re-run; nothing has been committed." >&2
        exit 1
    fi
    if offender=$(grep -iE 'generated with' <<<"$part"); then
        echo "ERROR: commit message contains agent attribution text:" >&2
        echo "  $offender" >&2
        echo "Remove it and re-run; nothing has been committed." >&2
        exit 1
    fi
done

if ! printf '%s\n' "${msg_parts[@]}" | grep -qiE '^[[:space:]]*model:[[:space:]]*[^[:space:]]'; then
    echo "ERROR: commit message is missing a 'Model:' footer." >&2
    echo "  Example: -m \"Model: <model-name>\"" >&2
    echo "Nothing has been committed." >&2
    exit 1
fi

# Resolve reviewer context
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)
block=""
if [[ -n "$branch" ]]; then
    block=$(reviewer response-block "$branch" 2>/dev/null || true)
fi

if [[ -z "$block" ]]; then
    # No review block applies — behave identically to the original script.
    exec git commit --author='AI Agent <agent@example.com>' "${original_args[@]}"
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

for part in "${msg_parts[@]}"; do
    printf '%s\n\n' "$part" >> "$tmp"
done
printf '%s\n' "$block" >> "$tmp"

git commit --author='AI Agent <agent@example.com>' -F "$tmp" "${passthrough[@]}"
