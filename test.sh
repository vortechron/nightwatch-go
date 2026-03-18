#!/bin/bash
# Nightwatch Go E2E Test Script
# Usage: ./test.sh
set -e

PORT=9093
APP_DIR="$(cd "$(dirname "$0")" && pwd)"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}=== Nightwatch Go E2E Test ===${NC}"

# Kill any existing process on the port
lsof -ti:$PORT 2>/dev/null | xargs kill 2>/dev/null || true
sleep 1

# Build
echo -e "${YELLOW}Building...${NC}"
cd "$APP_DIR"
go build ./...
echo -e "${GREEN}Build OK${NC}"

# Start server
echo -e "${YELLOW}Starting server on :${PORT}...${NC}"
PORT=$PORT go run ./cmd/testserver/ > /tmp/nw_test.log 2>&1 &
SERVER_PID=$!
trap "kill $SERVER_PID 2>/dev/null; exit" INT TERM EXIT

# Wait for server
for i in $(seq 1 20); do
  if curl -s http://localhost:$PORT/health > /dev/null 2>&1; then
    echo -e "${GREEN}Server ready (${i}s)${NC}"
    break
  fi
  sleep 1
done

echo ""

# CRUD operations
echo -e "${CYAN}--- POST /tasks (INSERT query) ---${NC}"
curl -s -X POST http://localhost:$PORT/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Nightwatch Go test","description":"Timeline query stitching test"}' | jq .
echo ""

echo -e "${CYAN}--- POST /tasks (INSERT query) ---${NC}"
curl -s -X POST http://localhost:$PORT/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Second task","description":"More queries for timeline"}' | jq .
echo ""

echo -e "${CYAN}--- GET /tasks (SELECT query) ---${NC}"
curl -s http://localhost:$PORT/tasks | jq .
echo ""

echo -e "${CYAN}--- GET /tasks/1 (SELECT WHERE query) ---${NC}"
curl -s http://localhost:$PORT/tasks/1 | jq .
echo ""

echo -e "${CYAN}--- PUT /tasks/1 (SELECT + UPDATE queries) ---${NC}"
curl -s -X PUT http://localhost:$PORT/tasks/1 \
  -H 'Content-Type: application/json' \
  -d '{"completed":true}' | jq .
echo ""

echo -e "${CYAN}--- DELETE /tasks/2 (DELETE query) ---${NC}"
curl -s -X DELETE http://localhost:$PORT/tasks/2 | jq .
echo ""

echo -e "${CYAN}--- GET /error-test (exception record) ---${NC}"
curl -s http://localhost:$PORT/error-test | jq .
echo ""

# Wait for async ingestion
echo -e "${YELLOW}Waiting 10s for async ingestion to complete...${NC}"
sleep 10

echo -e "${GREEN}=== Server logs ===${NC}"
cat /tmp/nw_test.log

echo ""
echo -e "${GREEN}=== DONE ===${NC}"
echo "Check Omni dashboard: https://omni.vortechron.com"
echo "Look for requests with deploy='go-e2e-test' and check their timelines"
