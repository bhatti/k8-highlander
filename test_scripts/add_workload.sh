#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
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

# Create test CRD instances
create_test_crds() {
  info "Creating test CRDs..."

  # Create a process CRD
  $SCRIPT_DIR/crd_create_process.sh --name "test-process" --image "busybox:latest" \
    --max-restarts 5 --grace-period "60s"

  # Create a cronjob CRD
  $SCRIPT_DIR/crd_create_cronjob.sh --name "test-cronjob" --image "busybox:latest" \
    --schedule "*/5 * * * *"

  # Create a service CRD
  $SCRIPT_DIR/crd_create_service.sh --name "test-service" --image "nginx:alpine" \
    --replicas 2 --container-port 80 --service-port 8080

  # Create a persistent CRD
  $SCRIPT_DIR/crd_create_persistent.sh --name "test-persistent" --image "redis:alpine" \
    --replicas 1 --container-port 6379 --service-port 6380 \
    --volume-size "2Gi"

  info "Test CRDs created successfully"
}

# Main function
main() {
  info "Starting test CRD creation"

  create_test_crds

  info "Test setup complete. The CRDWatcher should now detect and sync these workloads."
  info "You can use crd_delete.sh to remove any of the test CRDs when finished testing."
}

# Execute main function
main
