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
IMAGE="nginx:alpine"
SHELL="/bin/sh"
REPLICAS=1
CONTAINER_PORT=80
SERVICE_PORT=8080
HEALTH_CHECK_PATH="/health"
HEALTH_CHECK_PORT=80
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
    --replicas)
      REPLICAS="$2"
      shift 2
      ;;
    --container-port)
      CONTAINER_PORT="$2"
      shift 2
      ;;
    --service-port)
      SERVICE_PORT="$2"
      shift 2
      ;;
    --health-check-path)
      HEALTH_CHECK_PATH="$2"
      shift 2
      ;;
    --health-check-port)
      HEALTH_CHECK_PORT="$2"
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
info "Creating Service CRD instance: $NAME in namespace $NAMESPACE"

cat > /tmp/k8-highlander/crds/service-${NAME}.yaml << EOF
apiVersion: highlander.plexobject.io/v1
kind: WorkloadService
metadata:
  name: ${NAME}
  namespace: ${NAMESPACE}
spec:
  image: "${IMAGE}"
  script:
    commands:
      - "nginx -g 'daemon off;'"
    shell: "${SHELL}"
  replicas: ${REPLICAS}
  ports:
    - name: "http"
      containerPort: ${CONTAINER_PORT}
      servicePort: ${SERVICE_PORT}
  env:
    TEST_ENV: "crd-value"
    CRD_DEFINED: "true"
    SERVICE_NAME: "${NAME}"
  resources:
    cpuRequest: "${CPU_REQUEST}"
    memoryRequest: "${MEMORY_REQUEST}"
    cpuLimit: "${CPU_LIMIT}"
    memoryLimit: "${MEMORY_LIMIT}"
  healthCheckPath: "${HEALTH_CHECK_PATH}"
  healthCheckPort: ${HEALTH_CHECK_PORT}
EOF

# Apply the CRD instance
kubectl apply -f /tmp/k8-highlander/crds/service-${NAME}.yaml
if [ $? -eq 0 ]; then
  info "Successfully created Service CRD instance: $NAME"
else
  error "Failed to create Service CRD instance: $NAME"
  exit 1
fi
