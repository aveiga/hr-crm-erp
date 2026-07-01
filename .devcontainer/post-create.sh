#!/usr/bin/env bash
# Runs once after the devcontainer is created: installs LibreOffice (for PDF
# generation) and warms the Go/Node dependency caches.
set -euo pipefail

echo "==> Installing LibreOffice (headless) + fonts"
sudo apt-get update
sudo apt-get install -y --no-install-recommends \
  libreoffice-writer-nogui fonts-liberation postgresql-client
sudo rm -rf /var/lib/apt/lists/*

echo "==> Creating the test database"
export PGPASSWORD=hrcrm
until pg_isready -h db -U hrcrm >/dev/null 2>&1; do sleep 1; done
psql -h db -U hrcrm -d hrcrm -tc "SELECT 1 FROM pg_database WHERE datname='hrcrm_test'" \
  | grep -q 1 || psql -h db -U hrcrm -d hrcrm -c "CREATE DATABASE hrcrm_test" || true

echo "==> Backend deps"
( cd backend && go mod download )

echo "==> Frontend deps"
( cd frontend && npm install )

echo "==> Done. Run the stack with:"
echo "    cd backend  && go run ./cmd/server"
echo "    cd frontend && npm run dev"
