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
SCHEDULE="*/1 * * * *"
RESTART_POLICY="OnFailure"
CPU_REQUEST="50m"
MEMORY_REQUEST="32Mi"
CPU_LIMIT="100m"
MEMORY_LIMIT="64Mi"

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
    --schedule)
      SCHEDULE="$2"
      shift 2
      ;;
    --restart-policy)
      RESTART_POLICY="$2"
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
info "Creating CronJob CRD instance: $NAME in namespace $NAMESPACE"

cat > /tmp/k8-highlander/crds/cronjob-${NAME}.yaml << EOF
apiVersion: highlander.plexobject.io/v1
kind: WorkloadCronJob
metadata:
  name: ${NAME}
  namespace: ${NAMESPACE}
spec:
  image: "${IMAGE}"
  script:
    commands:
      - "echo 'Running CRD-defined cron job: ${NAME} at \$(date)'"
      - "echo 'Hostname: \$(hostname)'"
      - "sleep 15"
    shell: "${SHELL}"
  schedule: "${SCHEDULE}"
  env:
    TEST_ENV: "crd-value"
    CRD_DEFINED: "true"
    CRONJOB_NAME: "${NAME}"
  resources:
    cpuRequest: "${CPU_REQUEST}"
    memoryRequest: "${MEMORY_REQUEST}"
    cpuLimit: "${CPU_LIMIT}"
    memoryLimit: "${MEMORY_LIMIT}"
  restartPolicy: "${RESTART_POLICY}"
EOF

# Apply the CRD instance
kubectl apply -f /tmp/k8-highlander/crds/cronjob-${NAME}.yaml
if [ $? -eq 0 ]; then
  info "Successfully created CronJob CRD instance: $NAME"
else
  error "Failed to create CronJob CRD instance: $NAME"
  exit 1
fi
