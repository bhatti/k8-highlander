#!/bin/bash

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

# Delete all test CRDs
delete_all_test_crds() {
  info "Deleting all test CRDs..."

  # Delete process CRDs
  ./crd_delete.sh --name "test-process" --kind WorkloadProcess || true

  # Delete cronjob CRDs
  ./crd_delete.sh --name "test-cronjob" --kind WorkloadCronJob || true

  # Delete service CRDs
  ./crd_delete.sh --name "test-service" --kind WorkloadService || true

  # Delete persistent CRDs
  ./crd_delete.sh --name "test-persistent" --kind WorkloadPersistent || true

  info "All test CRDs deleted"
}

# Stop all running controllers
stop_all_controllers() {
  info "Stopping all controllers..."

  ./run_controller.sh stop

  info "All controllers stopped"
}

# Clean up temporary files
clean_temp_files() {
  info "Cleaning up temporary files..."

  rm -f /tmp/k8-highlander/crds/process-test-*.yaml
  rm -f /tmp/k8-highlander/crds/cronjob-test-*.yaml
  rm -f /tmp/k8-highlander/crds/service-test-*.yaml
  rm -f /tmp/k8-highlander/crds/persistent-test-*.yaml

  info "Temporary files cleaned up"
}

# Main function
main() {
  info "Starting cleanup"

  stop_all_controllers
  delete_all_test_crds
  clean_temp_files

  info "Cleanup complete"
}

# Execute main function
main
