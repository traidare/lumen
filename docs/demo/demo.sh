#!/bin/bash
set -e
MCP_CONFIG='{"mcpServers":{"lumen":{"command":"./bin/lumen","args":["stdio"]}}}'
claude --print \
  --dangerously-skip-permissions \
  --mcp-config "$MCP_CONFIG" \
  --add-dir testdata/fixtures/go \
  --print "How does Observe() record a histogram value? Walk through the code path."
