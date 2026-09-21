#!/usr/bin/env bash
set -uo pipefail

# Resolve paths relative to this script
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/.." &>/dev/null && pwd)"

IMAGE_FILE="${1:-$SCRIPT_DIR/images.txt}"

if [[ ! -f "$IMAGE_FILE" ]]; then
  echo "Error: Image list '$IMAGE_FILE' not found." >&2
  echo "Usage: $0 [path/to/images.txt]" >&2
  exit 1
fi

total=0
passed=0
failed=0

echo "Starting checks using: $IMAGE_FILE"
echo "Project root: $ROOT_DIR"
echo "--------------------------------------------------"

while IFS= read -r line || [[ -n "$line" ]]; do
  # Trim leading and trailing whitespace
  image="$(echo "$line" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"

  # Skip empty lines and comments
  if [[ -z "$image" || "$image" =~ ^# ]]; then
    continue
  fi

  ((total++))
  printf "[%02d] Checking: %s ...\n" "$total" "$image"

  # Execute go run from the repository root
  if (cd "$ROOT_DIR" && go run cmd/shiphoist/main.go check "$image"); then
    ((passed++))
  else
    ((failed++))
    echo "  -> FAILED: $image" >&2
  fi

  echo "--------------------------------------------------"
done < "$IMAGE_FILE"

echo "Completed!"
echo "Total: $total | Passed: $passed | Failed: $failed"

# Exit with non-zero status code if any check failed
[[ "$failed" -eq 0 ]] || exit 1