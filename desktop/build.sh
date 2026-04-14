#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "==> Building Go binary..."
cd "$PROJECT_ROOT"
make build

echo "==> Copying binary to Tauri resources..."
cp "$PROJECT_ROOT/waggle" "$SCRIPT_DIR/src-tauri/resources/waggle"
chmod +x "$SCRIPT_DIR/src-tauri/resources/waggle"

echo "==> Building Tauri app..."
cd "$SCRIPT_DIR/src-tauri"
cargo tauri build

echo "==> Done!"
echo "    App: target/release/bundle/macos/Waggle.app"
echo "    DMG: target/release/bundle/dmg/"
