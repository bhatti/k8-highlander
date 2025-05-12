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
IMAGE="busybox:latest"
SHELL="/bin/sh"
RESTART_POLICY="Never"
MAX_RESTARTS=3
GRACE_PERIOD="30s"
CPU_REQUEST="100m"
MEMORY_REQUEST="64Mi"
CPU_LIMIT="200m"
MEMORY_LIMIT="128Mi"

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
    --image)
      IMAGE="$2"
      shift 2
      ;;
    --shell)
      SHELL="$2"
      shift 2
      ;;
    --restart-policy)
      RESTART_POLICY="$2"
      shift 2
      ;;
    --max-restarts)
      MAX_RESTARTS="$2"
      shift 2
      ;;
    --grace-period)
      GRACE_PERIOD="$2"
      shift 2
      ;;
    --cpu-request)
      CPU_REQUEST="$2"
      shift 2
      ;;
    --memory-request)
      MEMORY_REQUEST="$2"
      shift 2
      ;;
    --cpu-limit)
      CPU_LIMIT="$2"
      shift 2
      ;;
    --memory-limit)
      MEMORY_LIMIT="$2"
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

# Create CRD instance
info "Creating Process CRD instance: $NAME in namespace $NAMESPACE"

cat > /tmp/k8-highlander/crds/process-${NAME}.yaml << EOF
apiVersion: highlander.plexobject.io/v1
kind: WorkloadProcess
metadata:
  name: ${NAME}
  namespace: ${NAMESPACE}
spec:
  image: "${IMAGE}"
  script:
    commands:
      - "echo 'Starting CRD-defined process: ${NAME}'"
      - "echo 'Process ID: \$\$'"
      - "while true; do echo \"${NAME} heartbeat at \$(date)\"; sleep 10; done"
    shell: "${SHELL}"
  env:
    TEST_ENV: "crd-value"
    CRD_DEFINED: "true"
    PROCESS_NAME: "${NAME}"
  resources:
    cpuRequest: "${CPU_REQUEST}"
    memoryRequest: "${MEMORY_REQUEST}"
    cpuLimit: "${CPU_LIMIT}"
    memoryLimit: "${MEMORY_LIMIT}"
  restartPolicy: "${RESTART_POLICY}"
  maxRestarts: ${MAX_RESTARTS}
  gracePeriod: "${GRACE_PERIOD}"
EOF

# Apply the CRD instance
kubectl apply -f /tmp/k8-highlander/crds/process-${NAME}.yaml
if [ $? -eq 0 ]; then
  info "Successfully created Process CRD instance: $NAME"
else
  error "Failed to create Process CRD instance: $NAME"
  exit 1
fi
