#!/bin/bash -e
#sudo socat TCP-LISTEN:80,fork TCP:localhost:8080
#kubectl config current-context
#kubectl describe pod echo-process-2-pod -n default
#kubectl describe nodes
#kubectl get pod echo-process-2-pod -n default -o yaml | grep -A 10 resources

#test_scripts/setup_config.sh
#lsof -nP -iTCP:8080 | grep LISTEN
CONFIG_FILE="/tmp/k8-highlander/config/config.yaml"
CONTROLLER_ID=""
TENANT="test-tenant"
STORAGE_TYPE="redis"
REDIS_ADDR="localhost:6379"
KUBECONFIG="$HOME/.kube/config"
#PORT="8080"
NAMESPACE="default"
LOG_LEVEL="info"

# Check if we're running in GCP
if command -v gcloud &> /dev/null; then
    REDIS_IP=$(gcloud redis instances describe leader-election --region=us-central1 --format='value(host)' 2>/dev/null)
    if [ ! -z "$REDIS_IP" ]; then
        echo "GCP Redis instance detected at: $REDIS_IP"
        REDIS_ADDR="$REDIS_IP:6379"
    fi
fi

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

echo "Starting controller with ID: ${CONTROLLER_ID:-$(hostname)}"
echo "Tenant: $TENANT"
echo "Storage: $STORAGE_TYPE at $REDIS_ADDR"
echo "Kubeconfig: $KUBECONFIG"
echo "Namespace: $NAMESPACE"
#echo "Port: $PORT"

head $CONFIG_FILE
# Run the controller
go run cmd/main.go --config "$CONFIG_FILE"
