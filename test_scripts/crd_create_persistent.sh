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
IMAGE="redis:alpine"
SHELL="/bin/sh"
REPLICAS=1
CONTAINER_PORT=6379
SERVICE_PORT=6380
SERVICE_NAME=""
HEALTH_CHECK_PATH="/health"
HEALTH_CHECK_PORT=6379
VOLUME_SIZE="1Gi"
STORAGE_CLASS="standard"
CPU_REQUEST="100m"
MEMORY_REQUEST="128Mi"
CPU_LIMIT="200m"
MEMORY_LIMIT="256Mi"

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
    --service-name)
      SERVICE_NAME="$2"
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
    --volume-size)
      VOLUME_SIZE="$2"
      shift 2
      ;;
    --storage-class)
      STORAGE_CLASS="$2"
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

# If service name not provided, use NAME
if [ -z "$SERVICE_NAME" ]; then
  SERVICE_NAME="${NAME}-svc"
fi

# Create CRD instance
info "Creating Persistent CRD instance: $NAME in namespace $NAMESPACE"

cat > /tmp/k8-highlander/crds/persistent-${NAME}.yaml << EOF
apiVersion: highlander.plexobject.io/v1
kind: WorkloadPersistent
metadata:
  name: ${NAME}
  namespace: ${NAMESPACE}
spec:
  image: "${IMAGE}"
  script:
    commands:
      - "redis-server"
    shell: "${SHELL}"
  replicas: ${REPLICAS}
  ports:
    - name: "redis"
      containerPort: ${CONTAINER_PORT}
      servicePort: ${SERVICE_PORT}
  env:
    TEST_ENV: "crd-value"
    CRD_DEFINED: "true"
    PERSISTENT_NAME: "${NAME}"
  resources:
    cpuRequest: "${CPU_REQUEST}"
    memoryRequest: "${MEMORY_REQUEST}"
    cpuLimit: "${CPU_LIMIT}"
    memoryLimit: "${MEMORY_LIMIT}"
  serviceName: "${SERVICE_NAME}"
  healthCheckPath: "${HEALTH_CHECK_PATH}"
  healthCheckPort: ${HEALTH_CHECK_PORT}
  persistentVolumes:
    - name: "data"
      mountPath: "/data"
      size: "${VOLUME_SIZE}"
      storageClassName: "${STORAGE_CLASS}"
EOF

# Apply the CRD instance
kubectl apply -f /tmp/k8-highlander/crds/persistent-${NAME}.yaml
if [ $? -eq 0 ]; then
  info "Successfully created Persistent CRD instance: $NAME"
else
  error "Failed to create Persistent CRD instance: $NAME"
  exit 1
fi
