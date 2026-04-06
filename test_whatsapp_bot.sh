#!/bin/bash

# WhatsApp Bot v0.1 - Test Script
# Usage: ./test_whatsapp_bot.sh

set -e

echo "🤖 WhatsApp Bot v0.1 - Test Suite"
echo "=================================="

# Check Go is installed
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed"
    exit 1
fi

echo "✅ Go version: $(go version | cut -d ' ' -f 3)"

# Build whatsapp package
echo ""
echo "📦 Building whatsapp package..."
if go build ./api/gateway/whatsapp 2>&1; then
    echo "✅ Build successful"
else
    echo "❌ Build failed"
    exit 1
fi

# Run tests
echo ""
echo "🧪 Running tests..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Test parser
echo ""
echo "Test 1: Parser Tests"
go test ./api/gateway/whatsapp -v -run TestParseIntent -timeout 5s

# Test formatter
echo ""
echo "Test 2: Formatter Tests"
go test ./api/gateway/whatsapp -v -run TestFormatResponse -timeout 5s

# Test bot
echo ""
echo "Test 3: Bot Logic Tests"
go test ./api/gateway/whatsapp -v -run TestHandleIntent -timeout 5s

# Run all tests
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Running ALL whatsapp tests..."
go test ./api/gateway/whatsapp -v

# Show test coverage
echo ""
echo "📊 Coverage:"
go test ./api/gateway/whatsapp -cover

echo ""
echo "=================================="
echo "✅ All tests passed!"
echo "=================================="
echo ""
echo "Next steps:"
echo "1. Run: go run api/main.go (with api.LocalTestBot() in main)"
echo "2. Test curl requests (see WHATSAPP_BOT_README.md)"
echo "3. Get WhatsApp Business API credentials"
echo ""
