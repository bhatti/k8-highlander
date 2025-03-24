#!/bin/bash -ex
#test_scripts/setup_config.sh

CONFIG_FILE="/tmp/k8-highlander/config/config.yaml"
CONTROLLER_ID=""
TENANT="test-tenant"
REDIS_ADDR="localhost:6379"
KUBECONFIG="$HOME/.kube/config"
#PORT="8080"
NAMESPACE="default"
LOG_LEVEL="info"
STORAGE_TYPE="db"

# Check for kubeconfig files
if [ -f "./primary-kubeconfig.yaml" ]; then
    KUBECONFIG="$(pwd)/primary-kubeconfig.yaml"
    echo "Using primary kubeconfig: $KUBECONFIG"
fi


# Set environment variables
export HIGHLANDER_ID="$CONTROLLER_ID"
export HIGHLANDER_TENANT="$TENANT"
export HIGHLANDER_STORAGE_TYPE="$STORAGE_TYPE"
export HIGHLANDER_REDIS_ADDR="$REDIS_ADDR"
export HIGHLANDER_KUBECONFIG="$KUBECONFIG"
#export HIGHLANDER_PORT="$PORT"
export HIGHLANDER_NAMESPACE="$NAMESPACE"
export HIGHLANDER_DATABASE_URL="postgres://pguser:pguser@localhost:5432/db?sslmode=require"

echo "Starting controller with ID: ${CONTROLLER_ID:-$(hostname)}"
echo "Tenant: $TENANT"
echo "Storage: $STORAGE_TYPE at $REDIS_ADDR"
echo "Kubeconfig: $KUBECONFIG"
echo "Namespace: $NAMESPACE"
#echo "Port: $PORT"

# Run the controller
go run cmd/main.go --config "$CONFIG_FILE"

