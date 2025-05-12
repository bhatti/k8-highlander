#!/bin/bash

# ./list_crds.sh --namespace default --delete-all --confirm
# ./list_crds.sh
# Colors for prettier output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
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

# Print a header
header() {
  echo -e "${BLUE}=== $1 ===${NC}"
}

# Default values
NAMESPACE=""
DELETE_ALL=false
CONFIRM=false

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --namespace|-n)
      NAMESPACE="$2"
      shift 2
      ;;
    --delete-all)
      DELETE_ALL=true
      shift
      ;;
    --confirm|-y)
      CONFIRM=true
      shift
      ;;
    --help|-h)
      echo "Usage: $0 [OPTIONS]"
      echo "List or delete Highlander CRD instances"
      echo ""
      echo "Options:"
      echo "  -n, --namespace NAMESPACE  Specify a namespace (default: all namespaces)"
      echo "  --delete-all               Delete all Highlander CRD instances"
      echo "  -y, --confirm              Skip confirmation prompt when deleting"
      echo "  -h, --help                 Show this help message"
      exit 0
      ;;
    *)
      error "Unknown option: $1"
      echo "Use --help for usage information"
      exit 1
      ;;
  esac
done

# Build namespace parameter for kubectl
NS_PARAM=""
if [ -n "$NAMESPACE" ]; then
  NS_PARAM="-n $NAMESPACE"
fi

# CRD kinds used by Highlander
CRD_KINDS=(
  "WorkloadProcess"
  "WorkloadService"
  "WorkloadCronJob"
  "WorkloadPersistent"
)

# Function to list CRD instances
list_crds() {
  local any_found=false

  for KIND in "${CRD_KINDS[@]}"; do
    header "Listing $KIND instances $NS_PARAM"

    # Get instances
    local instances
    if [ -z "$NAMESPACE" ]; then
      instances=$(kubectl get $KIND --all-namespaces -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name 2>/dev/null)
    else
      instances=$(kubectl get $KIND $NS_PARAM -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name 2>/dev/null)
    fi

    if [ $? -ne 0 ] || [ -z "$instances" ] || [[ "$instances" == *"No resources found"* ]]; then
      echo "No $KIND instances found"
    else
      echo "$instances"
      any_found=true
    fi
    echo ""
  done

  if [ "$any_found" = false ]; then
    info "No Highlander CRD instances found in the specified scope"
  fi

  return 0
}

# Function to delete all CRD instances
delete_all_crds() {
  local count=0
  local failures=0

  for KIND in "${CRD_KINDS[@]}"; do
    header "Deleting $KIND instances $NS_PARAM"

    # Get instances
    local instances
    if [ -z "$NAMESPACE" ]; then
      instances=$(kubectl get $KIND --all-namespaces -o json | jq -r '.items[] | .metadata.namespace + " " + .metadata.name' 2>/dev/null)
    else
      instances=$(kubectl get $KIND $NS_PARAM -o json | jq -r '.items[] | .metadata.namespace + " " + .metadata.name' 2>/dev/null)
    fi

    # Skip if no instances or error
    if [ $? -ne 0 ] || [ -z "$instances" ]; then
      echo "No $KIND instances found"
      continue
    fi

    # Delete each instance
    while read -r line; do
      if [ -z "$line" ]; then
        continue
      fi

      read -r ns name <<< "$line"
      echo "Deleting $KIND $name in namespace $ns"

      kubectl delete $KIND $name -n $ns
      if [ $? -eq 0 ]; then
        ((count++))
        # Also remove the local YAML file if it exists
        local type_lower=$(echo $KIND | sed 's/Workload//g' | tr '[:upper:]' '[:lower:]')
        if [ -f "/tmp/k8-highlander/crds/${type_lower}-${name}.yaml" ]; then
          rm -f "/tmp/k8-highlander/crds/${type_lower}-${name}.yaml"
        fi
      else
        ((failures++))
        warning "Failed to delete $KIND $name in namespace $ns"
      fi
    done <<< "$instances"
  done

  # Summary
  if [ $count -gt 0 ]; then
    info "Successfully deleted $count Highlander CRD instance(s)"
  fi

  if [ $failures -gt 0 ]; then
    warning "$failures deletion(s) failed"
  fi

  return 0
}

# Main function
main() {
  # Check kubectl availability
  if ! command -v kubectl &> /dev/null; then
    error "kubectl could not be found, please install it first"
    exit 1
  fi

  # Check jq availability if we're going to delete
  if [ "$DELETE_ALL" = true ] && ! command -v jq &> /dev/null; then
    error "jq could not be found, please install it for deletion functionality"
    exit 1
  fi

  # Always list the CRDs first
  list_crds

  # Delete all CRDs if requested
  if [ "$DELETE_ALL" = true ]; then
    # Confirm unless --confirm/-y was specified
    if [ "$CONFIRM" != true ]; then
      echo ""
      read -p "Are you sure you want to delete all Highlander CRD instances? [y/N] " response
      if [[ ! "$response" =~ ^[Yy]$ ]]; then
        info "Operation cancelled by user"
        exit 0
      fi
    fi

    echo ""
    info "Deleting all Highlander CRD instances..."
    delete_all_crds
  fi
}

# Execute main function
main
