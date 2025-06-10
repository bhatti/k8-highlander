#!/bin/bash
# test-real-spanner.sh - Test Cloud Function with real Spanner

set -e

PROJECT_ID=${PROJECT_ID:-$(gcloud config get-value project)}
FUNCTION_NAME=${FUNCTION_NAME:-highlander-api}
REGION=${REGION:-us-central1}

echo "🧪 Testing Cloud Function with Real Spanner"
echo "============================================"
echo "Project: $PROJECT_ID"
echo "Function: $FUNCTION_NAME"
echo "Region: $REGION"
echo ""

# Try to get production function URL first, then fallback to dev
echo "🔍 Getting function URL..."
FUNCTION_URL=$(gcloud functions describe $FUNCTION_NAME \
    --region=$REGION \
    --project=$PROJECT_ID \
    --format="value(serviceConfig.uri)" 2>/dev/null) || \
FUNCTION_URL=$(gcloud functions describe ${FUNCTION_NAME}-dev \
    --region=$REGION \
    --project=$PROJECT_ID \
    --format="value(serviceConfig.uri)" 2>/dev/null) || {
    echo "❌ No function found. Deploy first with: make deploy-dev"
    exit 1
}

echo "✅ Function URL: $FUNCTION_URL"
echo ""

# Test health endpoint
echo "🏥 Testing health endpoint..."
if curl -f -s "$FUNCTION_URL/healthz" >/dev/null; then
    echo "✅ Health check passed"
else
    echo "❌ Health check failed"
    exit 1
fi

# Test database connection
echo "🗄️  Testing database connection..."
DB_INFO=$(curl -f -s "$FUNCTION_URL/api/dbinfo")
echo "$DB_INFO" | jq .

if echo "$DB_INFO" | jq -r '.data.connected' | grep -q "true"; then
    echo "✅ Database connection successful"
    DB_TYPE=$(echo "$DB_INFO" | jq -r '.data.type')
    DB_ADDRESS=$(echo "$DB_INFO" | jq -r '.data.address')
    echo "   Type: $DB_TYPE"
    echo "   Address: $DB_ADDRESS"
else
    echo "❌ Database connection failed"
    echo "Check logs with: make logs-dev"
    exit 1
fi

# Test status endpoint
echo "📊 Testing status endpoint..."
STATUS_INFO=$(curl -f -s "$FUNCTION_URL/api/status")
echo "$STATUS_INFO" | jq .
echo "✅ Status endpoint working"

# Test workloads endpoint
echo "⚙️  Testing workloads endpoint..."
WORKLOADS_INFO=$(curl -f -s "$FUNCTION_URL/api/workloads")
echo "$WORKLOADS_INFO" | jq .
echo "✅ Workloads endpoint working"

# Test dashboard
echo "🖥️  Testing dashboard..."
if curl -f -s "$FUNCTION_URL/" | grep -q "<!DOCTYPE html>"; then
    echo "✅ Dashboard is accessible"
else
    echo "⚠️  Dashboard may not be working properly"
fi

echo ""
echo "🎉 All tests passed! Your Cloud Function is working with real Spanner."
echo ""
echo "Next steps:"
echo "  • Access dashboard: $FUNCTION_URL"
echo "  • View logs: make logs-dev"
echo "  • Check costs: make check-costs"
