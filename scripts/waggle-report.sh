#!/bin/bash
# Report progress or health to Waggle from external systems (Colony, crons, deploys)
# Usage:
#   waggle-report.sh progress <project_id> <source> "summary"
#   waggle-report.sh health <project_id> <green|yellow|red>
#   waggle-report.sh touch <project_id>
#
# Examples:
#   waggle-report.sh progress wg-94e9d07a9c8a colony "Wrote 3 articles for aileapers.com"
#   waggle-report.sh health wg-d2b49a green
#   waggle-report.sh touch wg-d2b49a

WAGGLE_API="${WAGGLE_API:-http://localhost:4740}"
CMD="${1:?Usage: waggle-report.sh <progress|health|touch> ...}"
PROJECT_ID="${2:?project_id required}"

case "$CMD" in
  progress)
    SOURCE="${3:?source required (user|colony|cron|deploy|test)}"
    SUMMARY="${4:?summary required}"
    DETAIL="${5:-}"
    curl -sf -X POST "$WAGGLE_API/api/progress" \
      -H 'Content-Type: application/json' \
      -d "$(printf '{"project_id":"%s","source":"%s","summary":"%s","detail":"%s"}' \
        "$PROJECT_ID" "$SOURCE" "$SUMMARY" "$DETAIL")"
    ;;
  health)
    HEALTH="${3:?health required (green|yellow|red)}"
    curl -sf -X PATCH "$WAGGLE_API/api/projects/$PROJECT_ID" \
      -H 'Content-Type: application/json' \
      -d "$(printf '{"health":"%s"}' "$HEALTH")"
    ;;
  touch)
    NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    curl -sf -X PATCH "$WAGGLE_API/api/projects/$PROJECT_ID" \
      -H 'Content-Type: application/json' \
      -d "$(printf '{"last_touched_at":"%s"}' "$NOW")"
    ;;
  *)
    echo "Unknown command: $CMD"
    echo "Usage: waggle-report.sh <progress|health|touch> <project_id> [args...]"
    exit 1
    ;;
esac
