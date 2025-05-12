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

# Default values
NAMESPACE="default"
NAME=""
KIND=""

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --name)
      NAME="$2"
      shift 2
      ;;
    --namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    --kind)
      KIND="$2"
      shift 2
      ;;
    *)
      error "Unknown option: $1"
      exit 1
      ;;
  esac
done

# Check required parameters
if [ -z "$NAME" ]; then
  error "Name is required. Please provide --name parameter."
  exit 1
fi

if [ -z "$KIND" ]; then
  error "Kind is required. Please provide --kind parameter (WorkloadProcess, WorkloadCronJob, WorkloadService, or WorkloadPersistent)."
  exit 1
fi

# Validate KIND parameter
case $KIND in
  WorkloadProcess|workloadprocess)
    KIND="WorkloadProcess"
    ;;
  WorkloadCronJob|workloadcronjob)
    KIND="WorkloadCronJob"
    ;;
  WorkloadService|workloadservice)
    KIND="WorkloadService"
    ;;
  WorkloadPersistent|workloadpersistent)
    KIND="WorkloadPersistent"
    ;;
  *)
    error "Invalid kind: $KIND. Must be one of: WorkloadProcess, WorkloadCronJob, WorkloadService, or WorkloadPersistent."
    exit 1
    ;;
esac

# Delete the CRD instance
info "Deleting $KIND instance: $NAME from namespace $NAMESPACE"

kubectl delete $KIND $NAME -n $NAMESPACE
if [ $? -eq 0 ]; then
  info "Successfully deleted $KIND instance: $NAME"
else
  error "Failed to delete $KIND instance: $NAME"
  exit 1
fi

# Also remove the local YAML file if it exists
for TYPE in process cronjob service persistent; do
  if [ -f "/tmp/k8-highlander/crds/${TYPE}-${NAME}.yaml" ]; then
    rm -f "/tmp/k8-highlander/crds/${TYPE}-${NAME}.yaml"
    info "Removed local YAML file for $NAME"
  fi
done
