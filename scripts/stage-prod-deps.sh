#!/bin/bash
set -e
cd "$(dirname "$0")/.."
rm -rf prod_node_modules
mkdir -p prod_node_modules
cp package.json package-lock.json prod_node_modules/
cd prod_node_modules
npm ci --omit=dev
rm package.json package-lock.json
echo "Production dependencies staged."
