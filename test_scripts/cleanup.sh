#!/bin/bash

echo "Stopping all controllers..."
pkill -f "k8-highlander" || true

echo "Removing controller logs and PID files..."
rm -rf /tmp/k8-highlander

echo "Cleaning up workloads..."
kubectl delete pods,deployments,services,cronjobs,statefulsets -l managed-by=k8-highlander -n default --wait=false || true

# Wait for resources to be deleted
echo "Waiting for resources to be deleted..."
kubectl wait --for=delete pods,deployments,services,cronjobs,statefulsets -l managed-by=k8-highlander -n default --timeout=30s || true

# Force delete any remaining pods
REMAINING_PODS=$(kubectl get pods -l managed-by=k8-highlander -n default -o name 2>/dev/null)
if [ ! -z "$REMAINING_PODS" ]; then
    echo "Force deleting remaining pods..."
    kubectl delete pods -l managed-by=k8-highlander -n default --grace-period=0 --force || true
fi

echo "Stopping Redis container..."
if docker ps | grep -q highlander-test-redis; then
    docker stop highlander-test-redis || true
    docker rm highlander-test-redis || true
fi

echo "Stopping PostgreSQL container..."
if docker ps | grep -q highlander-test-postgres; then
    docker stop highlander-test-postgres || true
    docker rm highlander-test-postgres || true
fi

echo "Deleting kind cluster if it exists..."
if command -v kind &> /dev/null; then
    if kind get clusters | grep -q "highlander-test"; then
        kind delete cluster --name highlander-test
    fi
fi

kubectl delete deployments,statefulsets,replicasets,daemonsets -l managed-by=k8-highlander
kubectl delete pods -l managed-by=k8-highlander --grace-period=0 --force

echo "Cleanup complete."
