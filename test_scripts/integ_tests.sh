#!/bin/bash
# Integration test script for k8-highlander
# This script sets up the necessary environment and runs integration tests

set -e  # Exit on any error

# Color codes for better output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
info() {
    echo -e "${BLUE}INFO: $1${NC}"
}

success() {
    echo -e "${GREEN}SUCCESS: $1${NC}"
}

warning() {
    echo -e "${YELLOW}WARNING: $1${NC}"
}

error() {
    echo -e "${RED}ERROR: $1${NC}"
}

# Check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Redis container name
REDIS_CONTAINER_NAME="k8-highlander-redis-test"

# Redis cleanup function
cleanup_redis() {
    if [ "$USE_LOCAL_REDIS" = true ]; then
        info "Cleaning up Redis container..."
        if docker ps -a | grep -q $REDIS_CONTAINER_NAME; then
            docker stop $REDIS_CONTAINER_NAME >/dev/null 2>&1 || true
            docker rm $REDIS_CONTAINER_NAME >/dev/null 2>&1 || true
            success "Redis container cleaned up"
        fi
    fi
}

# Register cleanup function to run on script exit
trap cleanup_redis EXIT

# Detect and set up kubeconfig
detect_kubeconfig() {
    if [ -f "./primary-kubeconfig.yaml" ]; then
        export KUBECONFIG="./primary-kubeconfig.yaml"
        info "Using local primary-kubeconfig.yaml"
    elif [ -f "$HOME/.kube/config" ]; then
        export KUBECONFIG="$HOME/.kube/config"
        info "Using default ~/.kube/config"
    else
        error "No kubeconfig found. Please set up your Kubernetes configuration."
        exit 1
    fi

    # Verify kubernetes connection
    info "Verifying Kubernetes connection..."
    if ! kubectl get nodes >/dev/null 2>&1; then
        error "Cannot connect to Kubernetes cluster. Please check your kubeconfig."
        exit 1
    fi
    success "Kubernetes connection verified"

    # Set HIGHLANDER_KUBECONFIG env var for the tests
    export HIGHLANDER_KUBECONFIG="$KUBECONFIG"
}

# Setup CRD test resources
setup_crd_resources() {
    info "Setting up CRD test resources..."

    # Create test namespace if it doesn't exist
    TEST_NAMESPACE="highlander-test"
    if ! kubectl get namespace $TEST_NAMESPACE >/dev/null 2>&1; then
        kubectl create namespace $TEST_NAMESPACE
        success "Created test namespace: $TEST_NAMESPACE"
    else
        info "Using existing test namespace: $TEST_NAMESPACE"
    fi

    # Create test CRDs for k8-highlander
    info "Creating test CRDs for k8-highlander..."

    # WorkloadProcess CRD
    cat <<EOF | kubectl apply -f -
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workloadprocesses.highlander.plexobject.io
spec:
  group: highlander.plexobject.io
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                image:
                  type: string
                script:
                  type: object
                  properties:
                    commands:
                      type: array
                      items:
                        type: string
                    shell:
                      type: string
                env:
                  type: object
                  additionalProperties:
                    type: string
                resources:
                  type: object
                  properties:
                    cpuRequest:
                      type: string
                    memoryRequest:
                      type: string
                    cpuLimit:
                      type: string
                    memoryLimit:
                      type: string
                restartPolicy:
                  type: string
                maxRestarts:
                  type: integer
                gracePeriod:
                  type: string
  scope: Namespaced
  names:
    plural: workloadprocesses
    singular: workloadprocess
    kind: WorkloadProcess
    shortNames:
    - wp
EOF

    # WorkloadCronJob CRD
    cat <<EOF | kubectl apply -f -
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workloadcronjobs.highlander.plexobject.io
spec:
  group: highlander.plexobject.io
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                image:
                  type: string
                script:
                  type: object
                  properties:
                    commands:
                      type: array
                      items:
                        type: string
                    shell:
                      type: string
                schedule:
                  type: string
                env:
                  type: object
                  additionalProperties:
                    type: string
                resources:
                  type: object
                  properties:
                    cpuRequest:
                      type: string
                    memoryRequest:
                      type: string
                    cpuLimit:
                      type: string
                    memoryLimit:
                      type: string
                restartPolicy:
                  type: string
  scope: Namespaced
  names:
    plural: workloadcronjobs
    singular: workloadcronjob
    kind: WorkloadCronJob
    shortNames:
    - wcj
EOF

    # WorkloadService CRD
    cat <<EOF | kubectl apply -f -
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workloadservices.highlander.plexobject.io
spec:
  group: highlander.plexobject.io
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                image:
                  type: string
                script:
                  type: object
                  properties:
                    commands:
                      type: array
                      items:
                        type: string
                    shell:
                      type: string
                replicas:
                  type: integer
                ports:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      containerPort:
                        type: integer
                      servicePort:
                        type: integer
                env:
                  type: object
                  additionalProperties:
                    type: string
                resources:
                  type: object
                  properties:
                    cpuRequest:
                      type: string
                    memoryRequest:
                      type: string
                    cpuLimit:
                      type: string
                    memoryLimit:
                      type: string
                healthCheckPath:
                  type: string
                healthCheckPort:
                  type: integer
  scope: Namespaced
  names:
    plural: workloadservices
    singular: workloadservice
    kind: WorkloadService
    shortNames:
    - ws
EOF

    # Create test CRD instances
    info "Creating test CRD instances..."

    # Test WorkloadProcess instance
    cat <<EOF | kubectl apply -f -
apiVersion: highlander.plexobject.io/v1
kind: WorkloadProcess
metadata:
  name: test-process-crd
  namespace: $TEST_NAMESPACE
spec:
  image: busybox:crd-version
  script:
    commands:
      - "echo 'Starting CRD-defined process'"
      - "echo 'Process ID: \$\$'"
      - "sleep 300"
    shell: "/bin/sh"
  env:
    CRD_CONFIG: "from-crd"
  resources:
    cpuRequest: "100m"
    memoryRequest: "64Mi"
    cpuLimit: "200m"
    memoryLimit: "128Mi"
  restartPolicy: "Never"
  maxRestarts: 3
  gracePeriod: "30s"
EOF

    # Test WorkloadCronJob instance
    cat <<EOF | kubectl apply -f -
apiVersion: highlander.plexobject.io/v1
kind: WorkloadCronJob
metadata:
  name: test-cronjob-crd
  namespace: $TEST_NAMESPACE
spec:
  image: busybox:crd-version
  script:
    commands:
      - "echo 'Running CRD-defined cron job'"
      - "echo 'Running at: \$(date)'"
      - "sleep 10"
    shell: "/bin/sh"
  schedule: "*/5 * * * *"
  env:
    CRD_CONFIG: "from-crd"
  resources:
    cpuRequest: "50m"
    memoryRequest: "32Mi"
    cpuLimit: "100m"
    memoryLimit: "64Mi"
  restartPolicy: "OnFailure"
EOF

    # Test WorkloadService instance
    cat <<EOF | kubectl apply -f -
apiVersion: highlander.plexobject.io/v1
kind: WorkloadService
metadata:
  name: test-service-crd
  namespace: $TEST_NAMESPACE
spec:
  image: nginx:alpine
  script:
    commands:
      - "nginx -g 'daemon off;'"
    shell: "/bin/sh"
  replicas: 2
  ports:
    - name: http
      containerPort: 80
      servicePort: 8080
  env:
    CRD_CONFIG: "from-crd"
  resources:
    cpuRequest: "100m"
    memoryRequest: "64Mi"
    cpuLimit: "200m"
    memoryLimit: "128Mi"
  healthCheckPath: "/health"
  healthCheckPort: 80
EOF

    # Verify CRDs were created
    if kubectl get crd workloadprocesses.highlander.plexobject.io >/dev/null 2>&1 && \
       kubectl get crd workloadcronjobs.highlander.plexobject.io >/dev/null 2>&1 && \
       kubectl get crd workloadservices.highlander.plexobject.io >/dev/null 2>&1; then
        success "CRDs created successfully"
    else
        error "Failed to create CRDs"
        exit 1
    fi

    # Verify CRD instances
    if kubectl get workloadprocess test-process-crd -n $TEST_NAMESPACE >/dev/null 2>&1 && \
       kubectl get workloadcronjob test-cronjob-crd -n $TEST_NAMESPACE >/dev/null 2>&1 && \
       kubectl get workloadservice test-service-crd -n $TEST_NAMESPACE >/dev/null 2>&1; then
        success "CRD instances created successfully"
    else
        error "Failed to create CRD instances"
        exit 1
    fi

    # Set test namespace for the tests
    export HIGHLANDER_NAMESPACE="$TEST_NAMESPACE"
}

# Detect and set up Redis
setup_redis() {
    # Try to use GCP Redis if gcloud is available
    if command_exists gcloud; then
        info "Trying to detect GCP Redis instance..."
        if gcloud redis instances describe leader-election --region=us-central1 --format='value(host)' >/dev/null 2>&1; then
            REDIS_IP=$(gcloud redis instances describe leader-election --region=us-central1 --format='value(host)')
            info "Using GCP Redis instance at $REDIS_IP"
            USE_LOCAL_REDIS=false
        else
            warning "No GCP Redis instance found, will use local Docker Redis"
            USE_LOCAL_REDIS=true
        fi
    else
        warning "gcloud not found, will use local Docker Redis"
        USE_LOCAL_REDIS=true
    fi

    # Start local Redis if needed
    if [ "$USE_LOCAL_REDIS" = true ]; then
        info "Setting up local Redis container..."

        # Check if Redis container is already running
        if docker ps | grep -q $REDIS_CONTAINER_NAME; then
            info "Redis container already running"
        else
            # Check if container exists but is stopped
            if docker ps -a | grep -q $REDIS_CONTAINER_NAME; then
                info "Starting existing Redis container..."
                docker start $REDIS_CONTAINER_NAME
            else
                info "Creating new Redis container..."
                docker run --name $REDIS_CONTAINER_NAME -d -p 6379:6379 redis:alpine
            fi
            sleep 2  # Give Redis a moment to start
        fi

        # When using Docker locally, we should connect via localhost
        REDIS_IP="localhost"
        info "Using localhost for Redis connection (Docker port mapping)"
    fi

    info "Redis IP: $REDIS_IP"

    # Verify Redis connection
    info "Verifying Redis connection..."
    if command_exists redis-cli; then
        if ! redis-cli -h $REDIS_IP ping | grep -q "PONG"; then
            error "Redis connection failed"
            exit 1
        fi
        success "Redis connection verified"
    else
        warning "redis-cli not found, skipping Redis connection verification"
    fi

    export HIGHLANDER_REDIS_ADDR="$REDIS_IP:6379"
}

# Detect cluster name from kubeconfig
detect_cluster_name() {
    if [ -z "$HIGHLANDER_CLUSTER_NAME" ]; then
        if command_exists kubectl; then
            # Using kubectl to get the current context and cluster name
            info "Detecting cluster name using kubectl..."
            CURRENT_CONTEXT=$(kubectl config current-context 2>/dev/null || echo "")
            if [ ! -z "$CURRENT_CONTEXT" ]; then
                CLUSTER_NAME=$(kubectl config view --minify -o jsonpath='{.contexts[0].context.cluster}' 2>/dev/null || echo "")
                if [ ! -z "$CLUSTER_NAME" ]; then
                    export HIGHLANDER_CLUSTER_NAME="$CLUSTER_NAME"
                    info "Detected cluster name from kubectl: $CLUSTER_NAME"
                fi
            fi
        elif command_exists yq && [ -f "$KUBECONFIG" ]; then
            # Only use yq if kubectl is not available and kubeconfig file exists
            info "Detecting cluster name using yq..."
            CURRENT_CONTEXT=$(yq e '.current-context' "$KUBECONFIG" 2>/dev/null || echo "")
            if [ ! -z "$CURRENT_CONTEXT" ] && [ "$CURRENT_CONTEXT" != "null" ]; then
                CLUSTER_NAME=$(yq e ".contexts[] | select(.name == \"$CURRENT_CONTEXT\") | .context.cluster" "$KUBECONFIG" 2>/dev/null || echo "")
                if [ ! -z "$CLUSTER_NAME" ] && [ "$CLUSTER_NAME" != "null" ]; then
                    export HIGHLANDER_CLUSTER_NAME="$CLUSTER_NAME"
                    info "Detected cluster name from kubeconfig: $CLUSTER_NAME"
                fi
            fi
        else
            warning "Neither kubectl nor yq available for cluster name detection"
        fi

        # If still not set, fallback to default
        if [ -z "$HIGHLANDER_CLUSTER_NAME" ]; then
            export HIGHLANDER_CLUSTER_NAME="default"
            warning "Using default cluster name: $HIGHLANDER_CLUSTER_NAME"
        fi
    fi

    info "Using cluster name: $HIGHLANDER_CLUSTER_NAME"
}

# Set up environment variables
setup_env() {
    export HIGHLANDER_TEST_MODE="real"
    export HIGHLANDER_STORAGE_TYPE="redis"
    export HIGHLANDER_TENANT="test-tenant-$(date +%s)"  # Unique tenant name
    export HIGHLANDER_ID="test-controller-$(hostname)-$(date +%s)"  # Unique controller ID

    info "Environment variables set:"
    info "  HIGHLANDER_TEST_MODE=$HIGHLANDER_TEST_MODE"
    info "  HIGHLANDER_STORAGE_TYPE=$HIGHLANDER_STORAGE_TYPE"
    info "  HIGHLANDER_REDIS_ADDR=$HIGHLANDER_REDIS_ADDR"
    info "  HIGHLANDER_TENANT=$HIGHLANDER_TENANT"
    info "  HIGHLANDER_ID=$HIGHLANDER_ID"
    info "  HIGHLANDER_CLUSTER_NAME=$HIGHLANDER_CLUSTER_NAME"
    info "  HIGHLANDER_KUBECONFIG=$HIGHLANDER_KUBECONFIG"
    info "  HIGHLANDER_NAMESPACE=$HIGHLANDER_NAMESPACE"
}

# Run the tests
run_tests() {
    info "Running integration tests..."
    TEST_ARGS="-v -race"

    # Check if specific tests were requested
    if [ "$#" -gt 0 ]; then
        TEST_FILTER="$1"
        info "Running specific test(s): $TEST_FILTER"
        TEST_ARGS="$TEST_ARGS -run $TEST_FILTER"

        # Use go test directly with our specific test filter
        go test $TEST_ARGS ./pkg/leader/... -failfast
        #go test -v ./pkg/leader -run TestMultiClusterWithWorkloads
        #go test -v ./pkg/leader -failfast
    else
        # Run all CRD tests if no specific tests are provided
        TEST_FILTER="TestLeaderWithCRDWorkload|TestLeaderCRDFailover|TestMultipleCRDWorkloadTypes|TestCRDConfigUpdate"
        info "Running CRD tests: $TEST_FILTER"
        TEST_ARGS="$TEST_ARGS -run $TEST_FILTER"
        go test $TEST_ARGS ./pkg/leader/... -failfast

        # Optionally run the rest of the tests
        info "Running other integration tests..."
        make integ-test
    fi

    if [ $? -eq 0 ]; then
        success "Integration tests completed successfully!"
    else
        error "Integration tests failed!"
        exit 1
    fi
}

# Clean up test resources
cleanup_resources() {
    if [ -n "$TEST_NAMESPACE" ]; then
        info "Cleaning up test namespace and CRDs..."
        kubectl delete namespace $TEST_NAMESPACE --ignore-not-found

        # Delete CRDs
        kubectl delete crd workloadprocesses.highlander.plexobject.io --ignore-not-found
        kubectl delete crd workloadcronjobs.highlander.plexobject.io --ignore-not-found
        kubectl delete crd workloadservices.highlander.plexobject.io --ignore-not-found

        success "Test resources cleaned up"
    fi
}

# Register cleanup function to run on script exit
trap cleanup_resources EXIT

# Main execution
main() {
    info "Starting k8-highlander CRD integration tests"

    # Check prerequisites
    if ! command_exists docker; then
        warning "Docker not found. Redis may not be available for testing."
    fi

    if ! command_exists go; then
        error "Go not found. Please install Go to run the tests."
        exit 1
    fi

    if ! command_exists kubectl; then
        error "kubectl not found. Please install kubectl to run the CRD tests."
        exit 1
    fi

    detect_kubeconfig
    setup_redis
    setup_crd_resources # This is the key addition for CRD tests
    detect_cluster_name
    setup_env
    run_tests "$@"
}

# Run main function with all script arguments
main "$@"
