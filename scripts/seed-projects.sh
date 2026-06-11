#!/bin/bash
# Seed all projects into Waggle. Safe to re-run — skips existing by name match.
set -euo pipefail

API="http://localhost:4740/api/projects"
EXISTING=$(curl -s "$API" | python3 -c "import json,sys; [print(p.get('name','')) for p in json.load(sys.stdin)]" 2>/dev/null)

seed() {
  local name="$1" desc="$2" status="$3" account="$4" category="$5" health="$6" stack="$7" workdir="$8"
  if echo "$EXISTING" | grep -qx "$name"; then
    echo "SKIP: $name (exists)"
    return
  fi
  if [ ! -d "$workdir" ] && [ "$workdir" != "" ]; then
    echo "SKIP: $name (dir missing: $workdir)"
    return
  fi
  python3 -c "
import json, subprocess, sys
data = json.dumps({
    'name': '''$name''',
    'description': '''$desc''',
    'status': '''$status''',
    'account': '''$account''',
    'category': '''$category''',
    'health': '''$health''',
    'tech_stack': '''$stack''',
    'work_dir': '''$workdir'''
})
r = subprocess.run(
    ['curl', '-sf', '-X', 'POST', '$API', '-H', 'Content-Type: application/json', '-d', data],
    capture_output=True
)
if r.returncode == 0:
    print('OK: $name')
else:
    print('FAIL: $name')
"
}

echo "Seeding projects into Waggle..."

# CleanCoders projects
seed "cleancoders.com" "Clean Coders website" "active" "team" "cleancoders" "green" "clojure" "/Users/maniginam/projects/cleancoders/cleancoders.com"
seed "Epic" "Agile project management" "active" "team" "cleancoders" "unknown" "clojure" "/Users/maniginam/projects/cleancoders/epic"
seed "poker" "Planning poker" "active" "team" "cleancoders" "unknown" "clojure" "/Users/maniginam/projects/cleancoders/poker"
seed "Infrastructure" "CCS IaC meal/recipe pattern" "active" "team" "cleancoders" "green" "clojure" "/Users/maniginam/projects/cleancoders/infrastructure"
seed "c3kit" "C3Kit framework" "active" "team" "cleancoders" "green" "clojure" "/Users/maniginam/projects/cleancoders/c3kit"
seed "odyssey-cli" "Odyssey CLI tool" "dormant" "team" "cleancoders" "unknown" "clojure" "/Users/maniginam/projects/cleancoders/odyssey-cli"
seed "c3suite" "Executive Dashboard" "active" "team" "cleancoders" "yellow" "clojure" "/Users/maniginam/projects/c3suite"
seed "c3suite-studio" "C3Suite Studio" "dormant" "team" "cleancoders" "unknown" "clojure" "/Users/maniginam/projects/c3suite-studio"
seed "c3suite-video" "C3Suite Video" "dormant" "team" "cleancoders" "unknown" "clojure" "/Users/maniginam/projects/c3suite-video"
seed "CleanFlow" "CRM - KILLED June 2026" "killed" "team" "cleancoders" "unknown" "clojure" "/Users/maniginam/projects/cleancoders/cleanflow"
seed "CleanCraftsmen" "Training platform" "dormant" "team" "cleancoders" "unknown" "clojure" "/Users/maniginam/projects/CleanCraftsmen"

# Revenue projects
seed "Colony" "Autonomous AI daemon" "active" "pro" "revenue" "green" "clojure" "/Users/maniginam/projects/maniginam/colony"
seed "Passive Income" "KDP, Redbubble, affiliate sites" "earning" "pro" "revenue" "yellow" "python" "/Users/maniginam/projects/maniginam/passive-income"
seed "LegacyLeaps" "File migration SaaS" "active" "pro" "revenue" "green" "csharp" "/Users/maniginam/projects/maniginam/legacy-leaps"
seed "legacy-lift" "Legacy file upgrader" "active" "pro" "revenue" "unknown" "csharp" "/Users/maniginam/projects/maniginam/legacy-lift"
seed "invoice-ocr" "Invoice OCR SaaS" "earning" "pro" "revenue" "green" "python" "/Users/maniginam/projects/maniginam/invoice-ocr"
seed "AI Leapers Sites" "aileapers.com blog network" "earning" "pro" "revenue" "green" "html" "/Users/maniginam/passive-income/blogs"
seed "financial-freedom" "Financial tracking" "dormant" "pro" "revenue" "unknown" "python" "/Users/maniginam/projects/maniginam/financial-freedom"

# Infrastructure
seed "Waggle" "Agent coordination + context manager" "active" "pro" "infra" "green" "go" "/Users/maniginam/projects/maniginam/waggle"
seed "mcp-clojure-sdk" "MCP SDK for Clojure" "dormant" "pro" "infra" "unknown" "clojure" "/Users/maniginam/projects/mcp-clojure-sdk"
seed "squadron-comms-plugin" "Claude Code plugin" "dormant" "pro" "infra" "unknown" "typescript" "/Users/maniginam/projects/squadron-comms-plugin"
seed "adjutant" "Agent backend" "dormant" "pro" "infra" "unknown" "typescript" "/Users/maniginam/projects/adjutant"

# Personal projects
seed "Musicbox" "Music discovery platform" "active" "pro" "personal" "yellow" "typescript" "/Users/maniginam/projects/maniginam/musicbox"
seed "musicbox-ai" "Musicbox AI variant" "dormant" "pro" "personal" "unknown" "typescript" "/Users/maniginam/projects/maniginam/musicbox-ai"
seed "Pala" "Construction management" "dormant" "pro" "personal" "unknown" "clojure" "/Users/maniginam/projects/pala"
seed "life-ops" "Personal operations" "active" "pro" "personal" "unknown" "" "/Users/maniginam/projects/maniginam/life-ops"
seed "personas" "AI personas" "dormant" "pro" "personal" "unknown" "typescript" "/Users/maniginam/projects/maniginam/personas"
seed "maniginam.dev Sites" "Personal blog sites" "active" "pro" "personal" "green" "html" "/Users/maniginam/projects/maniginam/maniginam.github.io"
seed "wilson-warehouse" "Inventory management" "dormant" "pro" "personal" "unknown" "clojure" "/Users/maniginam/projects/wilson-warehouse"
seed "auto-reg" "Regulatory compliance" "dormant" "pro" "personal" "unknown" "clojure" "/Users/maniginam/projects/maniginam/auto-reg"

# Experimental
seed "Sideline" "Youth sports platform" "dormant" "pro" "experimental" "unknown" "clojure" "/Users/maniginam/projects/maniginam/sideline"
seed "TradeUp" "Apprenticeship marketplace" "dormant" "pro" "experimental" "unknown" "clojure" "/Users/maniginam/projects/maniginam/tradeup"
seed "HeirLine" "Heir property succession" "dormant" "pro" "experimental" "unknown" "clojure" "/Users/maniginam/projects/maniginam/heirline"
seed "hermes-agent" "Hermes agent" "dormant" "pro" "experimental" "unknown" "python" "/Users/maniginam/projects/maniginam/hermes-agent"
seed "hermes-content-pipeline" "Content pipeline" "dormant" "pro" "experimental" "unknown" "python" "/Users/maniginam/projects/maniginam/hermes-content-pipeline"
seed "crypto-ops" "Crypto operations" "dormant" "pro" "experimental" "unknown" "python" "/Users/maniginam/projects/maniginam/crypto-ops"
seed "3dkd3" "3D modeling" "dormant" "pro" "experimental" "unknown" "other" "/Users/maniginam/projects/maniginam/3dkd3"

# Learning/reference (dormant by nature)
seed "katas" "Programming katas" "dormant" "pro" "personal" "unknown" "clojure" "/Users/maniginam/projects/katas"
seed "clojure-koans" "Clojure learning" "dormant" "pro" "personal" "unknown" "clojure" "/Users/maniginam/projects/clojure-koans"
seed "speclj" "BDD framework" "dormant" "pro" "personal" "unknown" "clojure" "/Users/maniginam/projects/speclj"

echo ""
echo "Done. Check dashboard at http://localhost:4740"
