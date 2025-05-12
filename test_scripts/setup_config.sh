#!/bin/bash -e

# Colors for prettier output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Print an info message
info() {
  echo -e "${GREEN}[INFO]${NC} $1"
}

# Print a warning message
warning() {
  echo -e "${YELLOW}[WARNING]${NC} $1"
}

# Print an error message
error() {
  echo -e "${RED}[ERROR]${NC} $1"
}

# Configuration variables with sensible defaults
CONFIG_FILE="/tmp/k8-highlander/config/config.yaml"
CONTROLLER_ID="${1:-}"
PORT="${2:-8080}"
TENANT="test-tenant"
STORAGE_TYPE="${3:-redis}"
REDIS_ADDR="localhost:6379"
DATABASE_URL="${4:-}"
KUBECONFIG="$HOME/.kube/config"
NAMESPACE="default"
CLUSTER_NAME="default"

# Create required directories
mkdir -p /tmp/k8-highlander/config
mkdir -p /tmp/k8-highlander/logs
mkdir -p /tmp/k8-highlander/pids
mkdir -p /tmp/k8-highlander/crds

# Check if we're running in GCP
if command -v gcloud &> /dev/null; then
    REDIS_IP=$(gcloud redis instances describe leader-election --region=us-central1 --format='value(host)' 2>/dev/null)
    if [ ! -z "$REDIS_IP" ]; then
        info "GCP Redis instance detected at: $REDIS_IP"
        REDIS_ADDR="$REDIS_IP:6379"
    fi
fi

# Check if Redis is running locally in Docker (if not in GCP)
if [[ "$REDIS_ADDR" == "localhost:6379" ]] && command -v docker &> /dev/null; then
    # Check if redis container is already running
    if docker ps | grep -q redis-stack-server; then
        info "Redis container is already running"
    # Check if container exists but is stopped
    elif docker ps -a | grep -q redis-stack-server; then
        info "Redis container exists but is stopped, starting it"
        docker start redis-stack-server
        sleep 2
    else
        info "Starting new Redis container..."
        docker run -d --name redis-stack-server -p 6379:6379 redis/redis-stack-server:latest
        sleep 2
    fi

    # Verify Redis is actually running
    if ! docker ps | grep -q redis-stack-server; then
        warning "Redis container is not running properly"
    else
        info "Redis container is running on localhost:6379"
    fi
fi

# Check for kubeconfig files
if [ -f "./primary-kubeconfig.yaml" ]; then
    KUBECONFIG="$(pwd)/primary-kubeconfig.yaml"
    info "Using primary kubeconfig: $KUBECONFIG"
fi

# Try to detect cluster name from kubectl if available
if command -v kubectl &> /dev/null; then
    CURRENT_CONTEXT=$(kubectl config current-context 2>/dev/null || echo "")
    if [ ! -z "$CURRENT_CONTEXT" ]; then
        DETECTED_CLUSTER=$(kubectl config view --minify -o jsonpath='{.contexts[0].context.cluster}' 2>/dev/null || echo "")
        if [ ! -z "$DETECTED_CLUSTER" ]; then
            CLUSTER_NAME="$DETECTED_CLUSTER"
            info "Detected cluster name: $CLUSTER_NAME"
        fi
    fi
fi

# Create the config file based on storage type
if [ "$STORAGE_TYPE" == "redis" ]; then
    info "Creating Redis-based configuration file"
    cat > "$CONFIG_FILE" << EOF
id: "${CONTROLLER_ID}"
tenant: "${TENANT}"
port: ${PORT}
namespace: "${NAMESPACE}"

# Storage configuration
storageType: "redis"
redis:
  addr: "${REDIS_ADDR}"
  password: ""
  db: 0

# Cluster configuration
cluster:
  name: "${CLUSTER_NAME}"
  kubeconfig: "${KUBECONFIG}"

# Empty workloads configuration - CRDs will be detected and synced by the CRDWatcher
workloads:
  processes: []
  cronJobs: []
  services: []
  persistentSets: []
EOF

elif [ "$STORAGE_TYPE" == "db" ]; then
    if [ -z "$DATABASE_URL" ]; then
        error "Database URL is required for db storage type"
        exit 1
    fi

    info "Creating database-based configuration file"
    cat > "$CONFIG_FILE" << EOF
id: "${CONTROLLER_ID}"
tenant: "${TENANT}"
port: ${PORT}
namespace: "${NAMESPACE}"

# Storage configuration
storageType: "db"
databaseURL: "${DATABASE_URL}"

# Cluster configuration
cluster:
  name: "${CLUSTER_NAME}"
  kubeconfig: "${KUBECONFIG}"

# Empty workloads configuration - CRDs will be detected and synced by the CRDWatcher
workloads:
  processes: []
  cronJobs: []
  services: []
  persistentSets: []
EOF

else
    error "Unsupported storage type: $STORAGE_TYPE. Use 'redis' or 'db'"
    exit 1
fi

info "Configuration file created at $CONFIG_FILE"
info "Storage type: $STORAGE_TYPE"
if [ "$STORAGE_TYPE" == "redis" ]; then
    info "Redis address: $REDIS_ADDR"
else
    info "Database URL: $DATABASE_URL"
fi
info "Cluster name: $CLUSTER_NAME"
info "Kubeconfig: $KUBECONFIG"
info "Controller ID: ${CONTROLLER_ID:-<will be set at runtime>}"

# Export environment variables for easier usage by other scripts
echo "export HIGHLANDER_ID=\"$CONTROLLER_ID\"" > /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_TENANT=\"$TENANT\"" >> /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_STORAGE_TYPE=\"$STORAGE_TYPE\"" >> /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_REDIS_ADDR=\"$REDIS_ADDR\"" >> /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_DATABASE_URL=\"$DATABASE_URL\"" >> /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_KUBECONFIG=\"$KUBECONFIG\"" >> /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_NAMESPACE=\"$NAMESPACE\"" >> /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_CLUSTER_NAME=\"$CLUSTER_NAME\"" >> /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_PORT=\"$PORT\"" >> /tmp/k8-highlander/env.sh
echo "export HIGHLANDER_CONFIG_FILE=\"$CONFIG_FILE\"" >> /tmp/k8-highlander/env.sh

info "Environment variables saved to /tmp/k8-highlander/env.sh"
info "You can source this file before running the controller: source /tmp/k8-highlander/env.sh"
