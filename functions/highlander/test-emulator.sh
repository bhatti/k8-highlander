#!/bin/bash -x
# test-emulator.sh - Test Cloud Function with Spanner Emulator

set -e

EMULATOR_PROJECT=test-project
EMULATOR_INSTANCE=test-instance
EMULATOR_DATABASE=test-database

echo "🧪 Testing Cloud Function with Spanner Emulator"
echo "==============================================="
echo "This test runs everything locally and is completely free!"
echo ""

# Cleanup any existing processes
echo "🧹 Cleaning up existing processes..."
pkill -f "cloud-spanner-emulator" 2>/dev/null || true
pkill -f "funcframework" 2>/dev/null || true
pkill -f "go run" 2>/dev/null || true
pkill -f "main" 2>/dev/null || true
sleep 2

# Start Spanner emulator
echo "🚀 Starting Spanner emulator..."
gcloud emulators spanner start --host-port=localhost:9010 &
EMULATOR_PID=$!
echo "Emulator PID: $EMULATOR_PID"

# Wait for emulator to start
echo "⏳ Waiting for emulator to start..."
sleep 10

# Check if emulator is running
if ! curl -s http://localhost:9020/ >/dev/null; then
    echo "❌ Emulator failed to start"
    kill $EMULATOR_PID 2>/dev/null || true
    exit 1
fi
echo "✅ Emulator started successfully"

# Configure gcloud for emulator
echo "🔧 Configuring gcloud for emulator..."
gcloud config configurations activate emulator 2>/dev/null || gcloud config configurations create emulator
gcloud config set api_endpoint_overrides/spanner http://localhost:9020/ --configuration=emulator
gcloud config set auth/disable_credentials true --configuration=emulator
gcloud config set project $EMULATOR_PROJECT --configuration=emulator
gcloud config set account fake@example.com --configuration=emulator

# Create instance and database
echo "🏗️  Creating emulator instance and database..."
gcloud spanner instances create $EMULATOR_INSTANCE \
    --config=emulator-config \
    --description="Test Instance" \
    --nodes=1 \
    --configuration=emulator

gcloud spanner databases create $EMULATOR_DATABASE \
    --instance=$EMULATOR_INSTANCE \
    --configuration=emulator

# Create schema
echo "📋 Creating database schema..."
gcloud spanner databases ddl update $EMULATOR_DATABASE \
    --instance=$EMULATOR_INSTANCE \
    --configuration=emulator \
    --ddl='
CREATE TABLE leaders (
    id STRING(MAX) NOT NULL,
    tenant STRING(MAX) NOT NULL,
    namespace STRING(MAX) NOT NULL,
    cluster_name STRING(MAX) NOT NULL,
    leader_id STRING(MAX) NOT NULL,
    acquired_at TIMESTAMP NOT NULL,
    renewed_at TIMESTAMP NOT NULL,
    lease_duration INT64 NOT NULL,
    workload_version STRING(MAX),
    metadata JSON,
) PRIMARY KEY (id, tenant, namespace, cluster_name);

CREATE TABLE workloads (
    id STRING(MAX) NOT NULL,
    tenant STRING(MAX) NOT NULL,
    namespace STRING(MAX) NOT NULL,
    cluster_name STRING(MAX) NOT NULL,
    name STRING(MAX) NOT NULL,
    spec JSON NOT NULL,
    status JSON,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    version INT64 NOT NULL,
) PRIMARY KEY (id, tenant, namespace, cluster_name, name);'

echo "✅ Database schema created"

# Switch back to default gcloud config
gcloud config configurations activate default

# Create a standalone main.go that includes our function
echo "🚀 Creating standalone function runner..."
cat > test_main.go << 'EOF'
package main

import (
    "log"
    "net/http"
    "os"

    "github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

func main() {
    // Set the function target
    functions.HTTP("HighlanderAPI", HighlanderAPI)

    // Start the server
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    log.Printf("Starting server on port %s", port)
    if err := http.ListenAndServe(":"+port, http.DefaultServeMux); err != nil {
        log.Fatalf("ListenAndServe: %v", err)
    }
}
EOF

# Set environment variables
export SPANNER_EMULATOR_HOST=localhost:9010
export FUNCTION_TARGET=HighlanderAPI
export HIGHLANDER_STORAGE_TYPE=spanner
export HIGHLANDER_SPANNER_PROJECT_ID=$EMULATOR_PROJECT
export HIGHLANDER_SPANNER_INSTANCE_ID=$EMULATOR_INSTANCE
export HIGHLANDER_SPANNER_DATABASE_ID=$EMULATOR_DATABASE
export HIGHLANDER_ID=cf-local
export HIGHLANDER_TENANT=test
export HIGHLANDER_NAMESPACE=default
export HIGHLANDER_CLUSTER_NAME=test-cluster
export PORT=8080

echo "🔧 Environment variables set:"
echo "SPANNER_EMULATOR_HOST=$SPANNER_EMULATOR_HOST"
echo "HIGHLANDER_STORAGE_TYPE=$HIGHLANDER_STORAGE_TYPE"
echo "HIGHLANDER_SPANNER_PROJECT_ID=$HIGHLANDER_SPANNER_PROJECT_ID"

# Build and run the function
echo "🏗️  Building and starting function..."
go mod tidy
go run . test_main.go &
FUNCTION_PID=$!
echo "Function PID: $FUNCTION_PID"

# Wait for function to start
echo "⏳ Waiting for function to start..."
sleep 10

# Test the function
FUNCTION_URL="http://localhost:8080"
echo "🧪 Testing local function at $FUNCTION_URL"

# Test health endpoint with retries
echo "🏥 Testing health endpoint..."
HEALTH_OK=false
for i in {1..10}; do
    if curl -f -s "$FUNCTION_URL/healthz" >/dev/null 2>&1; then
        echo "✅ Health check passed"
        HEALTH_OK=true
        break
    else
        echo "⏳ Attempt $i/10 failed, retrying..."
        sleep 2
    fi
done

if [ "$HEALTH_OK" != "true" ]; then
    echo "❌ Health check failed after 10 attempts"
    echo "Function may not have started properly"
    # Try to get some debug info
    echo "Checking if process is running..."
    ps aux | grep -E "(go run|test_main)" || true
    kill $EMULATOR_PID $FUNCTION_PID 2>/dev/null || true
    exit 1
fi

# Test database connection
echo "🗄️  Testing database connection..."
DB_INFO=$(curl -f -s "$FUNCTION_URL/api/dbinfo" 2>/dev/null)
if [ $? -eq 0 ]; then
    echo "$DB_INFO" | jq .

    # Check if it's using Spanner
    if echo "$DB_INFO" | jq -r '.data.type' | grep -q "spanner"; then
        if echo "$DB_INFO" | jq -r '.data.connected' | grep -q "true"; then
            echo "✅ Spanner connection successful with emulator"
        else
            echo "❌ Spanner connection failed"
            echo "Check if emulator is accessible an
