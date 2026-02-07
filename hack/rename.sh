#!/usr/bin/env bash
# hack/rename.sh — Rename project from constraint-sentinel to nightjar.
# Run from the project root. Then delete this script.
set -euo pipefail

echo "=== Renaming: constraint-sentinel → nightjar ==="
echo ""

# ── Collect all text files ──
FILES=$(find . -type f \
    -not -path "./.git/*" \
    -not -path "./vendor/*" \
    -not -path "./bin/*" \
    -not -name "rename.sh" \
    -not -name "*.tar.gz" \
    -not -name "*.png" \
    -not -name "*.jpg")

text_replace() {
    local old="$1" new="$2" desc="$3"
    local count=0
    for f in $FILES; do
        if file "$f" | grep -q text 2>/dev/null; then
            if grep -q "$old" "$f" 2>/dev/null; then
                sed -i "s|${old}|${new}|g" "$f"
                count=$((count + 1))
            fi
        fi
    done
    printf "  %-55s %d files\n" "$desc" "$count"
}

# ── ORDER MATTERS: longest/most-specific strings first ──

echo "[1] Go module path"
text_replace \
    "github.com/yourorg/constraint-sentinel" \
    "github.com/nightjarctl/nightjar" \
    "github.com/yourorg/constraint-sentinel →"

echo "[2] MCP URI scheme"
text_replace \
    "constraint-sentinel://" \
    "nightjar://" \
    "constraint-sentinel:// →"

echo "[3] Prometheus metric prefix"
text_replace \
    "constraint_sentinel_" \
    "nightjar_" \
    "constraint_sentinel_ →"

echo "[4] CRD API group + annotation prefix"
text_replace \
    "constraint-sentinel.io" \
    "nightjar.io" \
    "constraint-sentinel.io →"

echo "[5] Kubernetes resource names (namespace, leader, webhook)"
text_replace \
    "constraint-sentinel-system" \
    "nightjar-system" \
    "constraint-sentinel-system →"

text_replace \
    "constraint-sentinel-leader" \
    "nightjar-leader" \
    "constraint-sentinel-leader →"

text_replace \
    "constraint-sentinel-webhook" \
    "nightjar-webhook" \
    "constraint-sentinel-webhook →"

echo "[6] Remaining hyphenated references"
text_replace \
    "constraint-sentinel" \
    "nightjar" \
    "constraint-sentinel →"

echo "[7] Display name (capitalized)"
text_replace \
    "Constraint Sentinel" \
    "Nightjar" \
    "Constraint Sentinel →"

# Note: "Sentinel" alone is NOT replaced globally — it would break
# words like "sentinel" in unrelated contexts. We rely on step 6+7
# catching the important ones.

echo "[8] Rename cmd/kubectl-sentinel directory"
if [ -d "cmd/kubectl-sentinel" ]; then
    mv cmd/kubectl-sentinel cmd/kubectl-nightjar
    echo "  cmd/kubectl-sentinel → cmd/kubectl-nightjar"
else
    echo "  (already renamed or doesn't exist)"
fi

echo ""
echo "=== Text replacement complete ==="
echo ""
echo "Manual steps:"
echo "  1. cd .. && mv constraint-sentinel nightjar && cd nightjar"
echo "  2. go mod edit -module github.com/nightjarctl/nightjar"
echo "  3. go mod tidy"
echo "  4. make build   # verify compilation"
echo "  5. make test    # verify tests pass"
echo "  6. grep -rn 'constraint.sentinel' .  # check for stragglers"
echo "  7. rm hack/rename.sh"
echo ""
echo "Decisions (update manually after verifying):"
echo "  - Container registry: Update 'image.repository' in deploy/helm/values.yaml"
echo "  - GitHub org/repo: Update git remote to github.com/nightjarctl/nightjar"
echo "  - Domain: Register nightjar.io if you want a real CRD API group domain"
echo "  - CLI name: Binary is 'nightjar'. kubectl plugin is 'kubectl-nightjar'."
echo "    Add 'nightjarctl' as a symlink/alias in Makefile if you want both."
