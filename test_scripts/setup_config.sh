#!/bin/bash -x

set -e

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

# Check if a command exists
command_exists() {
  command -v "$1" >/dev/null 2>&1
}

# Create required directories
create_directories() {
  info "Creating directories..."
  mkdir -p /tmp/k8-highlander/config
  mkdir -p /tmp/k8-highlander/logs
  mkdir -p /tmp/k8-highlander/pids
  mkdir -p /tmp/k8-highlander/crds
}

# Set up Redis
setup_redis() {
  # Check if we're running in GCP
  REDIS_ADDR="localhost:6379"
  if command_exists gcloud; then
    REDIS_IP=$(gcloud redis instances describe leader-election --region=us-central1 --format='value(host)' 2>/dev/null)
    if [ ! -z "$REDIS_IP" ]; then
      info "GCP Redis instance detected at: $REDIS_IP"
      REDIS_ADDR="$REDIS_IP:6379"
    fi
  else
    # Start Redis if not already running
    if ! docker ps | grep -q redis; then
      info "Starting Redis container..."
      docker run -d --name redis-stack-server -p 6379:6379 redis/redis-stack-server:latest
      sleep 2
    fi
  fi

  echo "$REDIS_ADDR"
}

# Determine kubeconfig path
determine_kubeconfig() {
  KUBECONFIG="$HOME/.kube/config"
  if [ -f "./primary-kubeconfig.yaml" ]; then
    KUBECONFIG="$(pwd)/primary-kubeconfig.yaml"
    info "Using primary kubeconfig: $KUBECONFIG"
  fi

  echo "$KUBECONFIG"
}

# Detect cluster name from kubeconfig
detect_cluster_name() {
  local kubeconfig=$1
  local detected_name=""

  if [ -z "$HIGHLANDER_CLUSTER_NAME" ]; then
    if command_exists kubectl; then
      # Using kubectl to get the cluster name (more reliable)
      CURRENT_CONTEXT=$(kubectl config current-context 2>/dev/null || echo "")
      if [ ! -z "$CURRENT_CONTEXT" ]; then
        detected_name=$(kubectl config view --minify -o jsonpath='{.contexts[0].context.cluster}' 2>/dev/null || echo "")
        if [ ! -z "$detected_name" ]; then
          export HIGHLANDER_CLUSTER_NAME="$detected_name"
        fi
      fi
    elif command_exists yq && [ -f "$kubeconfig" ]; then
      # Fallback to yq if kubectl is not available
      CURRENT_CONTEXT=$(yq e '.current-context' "$kubeconfig" 2>/dev/null || echo "")
      if [ ! -z "$CURRENT_CONTEXT" ] && [ "$CURRENT_CONTEXT" != "null" ]; then
        detected_name=$(yq e ".contexts[] | select(.name == \"$CURRENT_CONTEXT\") | .context.cluster" "$kubeconfig" 2>/dev/null || echo "")
        if [ ! -z "$detected_name" ] && [ "$detected_name" != "null" ]; then
          export HIGHLANDER_CLUSTER_NAME="$detected_name"
        fi
      fi
    fi

    # If still not set, fallback to default
    if [ -z "$HIGHLANDER_CLUSTER_NAME" ]; then
      detected_name="default"
      export HIGHLANDER_CLUSTER_NAME="default"
    else
      detected_name="$HIGHLANDER_CLUSTER_NAME"
    fi
  else
    detected_name="$HIGHLANDER_CLUSTER_NAME"
  fi

  # Return only the name, without any log messages
  echo "$detected_name"
}


# Create CRD definitions
create_crd_definitions() {
  info "Creating CRD definitions..."

  # Create WorkloadProcess CRD definition
  cat > /tmp/k8-highlander/crds/workload-process-crd.yaml << EOF
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workloadprocesses.highlander.plexobject.io
spec:
  group: highlander.plexobject.io
  names:
    plural: workloadprocesses
    singular: workloadprocess
    kind: WorkloadProcess
    shortNames:
      - wproc
  scope: Namespaced
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
EOF

  # Create WorkloadCronJob CRD definition
  cat > /tmp/k8-highlander/crds/workload-cronjob-crd.yaml << EOF
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workloadcronjobs.highlander.plexobject.io
spec:
  group: highlander.plexobject.io
  names:
    plural: workloadcronjobs
    singular: workloadcronjob
    kind: WorkloadCronJob
    shortNames:
      - wcron
  scope: Namespaced
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
EOF

  # Create WorkloadService CRD definition
  cat > /tmp/k8-highlander/crds/workload-service-crd.yaml << EOF
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workloadservices.highlander.plexobject.io
spec:
  group: highlander.plexobject.io
  names:
    plural: workloadservices
    singular: workloadservice
    kind: WorkloadService
    shortNames:
      - wsvc
  scope: Namespaced
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
EOF

  # Create WorkloadPersistent CRD definition
  cat > /tmp/k8-highlander/crds/workload-persistent-crd.yaml << EOF
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workloadpersistents.highlander.plexobject.io
spec:
  group: highlander.plexobject.io
  names:
    plural: workloadpersistents
    singular: workloadpersistent
    kind: WorkloadPersistent
    shortNames:
      - wpers
  scope: Namespaced
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
                serviceName:
                  type: string
                healthCheckPath:
                  type: string
                healthCheckPort:
                  type: integer
                persistentVolumes:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      mountPath:
                        type: string
                      size:
                        type: string
                      storageClassName:
                        type: string
EOF
}

# Apply CRD definitions to the cluster
apply_crd_definitions() {
  info "Applying CRD definitions..."

  kubectl apply -f /tmp/k8-highlander/crds/workload-process-crd.yaml || warning "Failed to apply WorkloadProcess CRD - continuing anyway"
  kubectl apply -f /tmp/k8-highlander/crds/workload-cronjob-crd.yaml || warning "Failed to apply WorkloadCronJob CRD - continuing anyway"
  kubectl apply -f /tmp/k8-highlander/crds/workload-service-crd.yaml || warning "Failed to apply WorkloadService CRD - continuing anyway"
  kubectl apply -f /tmp/k8-highlander/crds/workload-persistent-crd.yaml || warning "Failed to apply WorkloadPersistent CRD - continuing anyway"
}

# Create CRD instances
create_crd_instances() {
  info "Creating CRD instances..."

  # Create ProcessCRD instance
  cat > /tmp/k8-highlander/crds/process-crd-instance.yaml << EOF
apiVersion: highlander.plexobject.io/v1
kind: WorkloadProcess
metadata:
  name: crd-process
  namespace: default
spec:
  image: "busybox:latest"
  script:
    commands:
      - "echo 'Starting CRD-defined process'"
      - "echo 'Process ID: \$\$'"
      - "while true; do echo \"CRD process heartbeat at \$(date)\"; sleep 10; done"
    shell: "/bin/sh"
  env:
    TEST_ENV: "crd-value"
    CRD_DEFINED: "true"
  resources:
    cpuRequest: "100m"
    memoryRequest: "64Mi"
    cpuLimit: "200m"
    memoryLimit: "128Mi"
  restartPolicy: "Never"
  maxRestarts: 3
  gracePeriod: "30s"
EOF

  # Create CronJobCRD instance
  cat > /tmp/k8-highlander/crds/cronjob-crd-instance.yaml << EOF
apiVersion: highlander.plexobject.io/v1
kind: WorkloadCronJob
metadata:
  name: crd-cronjob
  namespace: default
spec:
  image: "busybox:latest"
  script:
    commands:
      - "echo 'Running CRD-defined cron job at \$(date)'"
      - "echo 'Hostname: \$(hostname)'"
      - "sleep 15"
    shell: "/bin/sh"
  schedule: "*/1 * * * *"
  env:
    TEST_ENV: "crd-value"
    CRD_DEFINED: "true"
  resources:
    cpuRequest: "50m"
    memoryRequest: "32Mi"
    cpuLimit: "100m"
    memoryLimit: "64Mi"
  restartPolicy: "OnFailure"
EOF

  # Create ServiceCRD instance
  cat > /tmp/k8-highlander/crds/service-crd-instance.yaml << EOF
apiVersion: highlander.plexobject.io/v1
kind: WorkloadService
metadata:
  name: crd-service
  namespace: default
spec:
  image: "nginx:alpine"
  script:
    commands:
      - "nginx -g 'daemon off;'"
    shell: "/bin/sh"
  replicas: 1
  ports:
    - name: "http"
      containerPort: 80
      servicePort: 8282
  env:
    TEST_ENV: "crd-value"
    CRD_DEFINED: "true"
  resources:
    cpuRequest: "100m"
    memoryRequest: "64Mi"
    cpuLimit: "200m"
    memoryLimit: "128Mi"
  healthCheckPath: "/health"
  healthCheckPort: 80
EOF

  # Create PersistentCRD instance
  cat > /tmp/k8-highlander/crds/persistent-crd-instance.yaml << EOF
apiVersion: highlander.plexobject.io/v1
kind: WorkloadPersistent
metadata:
  name: crd-persistent
  namespace: default
spec:
  image: "redis:alpine"
  script:
    commands:
      - "redis-server"
    shell: "/bin/sh"
  replicas: 1
  ports:
    - name: "redis"
      containerPort: 6379
      servicePort: 6380
  env:
    TEST_ENV: "crd-value"
    CRD_DEFINED: "true"
  resources:
    cpuRequest: "100m"
    memoryRequest: "128Mi"
    cpuLimit: "200m"
    memoryLimit: "256Mi"
  serviceName: "crd-persistent-svc"
  persistentVolumes:
    - name: "data"
      mountPath: "/data"
      size: "1Gi"
EOF
}

# Apply CRD instances to the cluster
apply_crd_instances() {
  info "Applying CRD instances..."

  kubectl apply -f /tmp/k8-highlander/crds/process-crd-instance.yaml || warning "Failed to apply Process CRD instance - continuing anyway"
  kubectl apply -f /tmp/k8-highlander/crds/cronjob-crd-instance.yaml || warning "Failed to apply CronJob CRD instance - continuing anyway"
  kubectl apply -f /tmp/k8-highlander/crds/service-crd-instance.yaml || warning "Failed to apply Service CRD instance - continuing anyway"
  kubectl apply -f /tmp/k8-highlander/crds/persistent-crd-instance.yaml || warning "Failed to apply Persistent CRD instance - continuing anyway"
}

# Create configuration file
create_config_file() {
  local id=$1
  local port=$2
  local redis_addr=$3
  local kubeconfig=$4
  local cluster_name=$5

  info "Creating configuration file..."

  # Ensure backslashes in the commands are properly escaped
  cat > /tmp/k8-highlander/config/config.yaml << 'EOF'
id: "ID_PLACEHOLDER"
tenant: "test-tenant"
port: PORT_PLACEHOLDER
namespace: "default"

# Storage configuration
storageType: "redis"
redis:
  addr: "REDIS_ADDR_PLACEHOLDER"
  password: ""
  db: 0

# Cluster configuration
cluster:
  name: "CLUSTER_NAME_PLACEHOLDER"
  kubeconfig: "KUBECONFIG_PLACEHOLDER"

# Workloads configuration
workloads:
  processes:
    # Regular process configuration
    - name: "singleton-echo"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Starting singleton process'"
          - "echo 'Process ID: $$'"
          - "while true; do echo \"Regular process heartbeat at $(date)\"; sleep 10; done"
        shell: "/bin/sh"
      env:
        TEST_ENV: "regular-value"
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"
      restartPolicy: "Never"

    # CRD-based process configuration
    - name: "crd-echo"
      # Minimal config since we'll get most from CRD
      image: "busybox:latest"  # Fallback image
      script:
        commands:
          - "echo 'Fallback script'"
        shell: "/bin/sh"
      workloadCRDRef:
        apiVersion: "highlander.plexobject.io/v1"
        kind: "WorkloadProcess"
        name: "crd-process"
        namespace: "default"

  cronJobs:
    # Regular cronjob configuration
    - name: "singleton-cron"
      schedule: "*/1 * * * *"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Running regular cron job at $(date)'"
          - "echo 'Hostname: $(hostname)'"
          - "sleep 15"
        shell: "/bin/sh"
      env:
        TEST_ENV: "regular-value"
      resources:
        cpuRequest: "50m"
        memoryRequest: "32Mi"
        cpuLimit: "100m"
        memoryLimit: "64Mi"
      restartPolicy: "OnFailure"

    # CRD-based cronjob configuration
    - name: "crd-cron"
      schedule: "*/5 * * * *"  # Fallback schedule
      image: "busybox:latest"  # Fallback image
      script:
        commands:
          - "echo 'Fallback script'"
        shell: "/bin/sh"
      workloadCRDRef:
        apiVersion: "highlander.plexobject.io/v1"
        kind: "WorkloadCronJob"
        name: "crd-cronjob"
        namespace: "default"

  services:
    # Regular service configuration
    - name: "singleton-service"
      replicas: 1
      image: "nginx:alpine"
      script:
        commands:
          - "nginx -g 'daemon off;'"
        shell: "/bin/sh"
      ports:
        - name: "http"
          containerPort: 80
          servicePort: 8181
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"

    # CRD-based service configuration
    - name: "crd-service-frontend"
      replicas: 1  # Fallback replicas
      image: "nginx:alpine"  # Fallback image
      script:
        commands:
          - "nginx -g 'daemon off;'"
        shell: "/bin/sh"
      workloadCRDRef:
        apiVersion: "highlander.plexobject.io/v1"
        kind: "WorkloadService"
        name: "crd-service"
        namespace: "default"

  persistentSets:
    # Regular persistent configuration
    - name: "singleton-persistent"
      replicas: 1
      image: "redis:alpine"
      script:
        commands:
          - "redis-server"
        shell: "/bin/sh"
      ports:
        - name: "redis"
          containerPort: 6379
          servicePort: 6379
      persistentVolumes:
        - name: "data"
          mountPath: "/data"
          size: "1Gi"
      resources:
        cpuRequest: "100m"
        memoryRequest: "128Mi"
        cpuLimit: "200m"
        memoryLimit: "256Mi"

    # CRD-based persistent configuration
    - name: "crd-persistent-db"
      replicas: 1  # Fallback replicas
      image: "redis:alpine"  # Fallback image
      script:
        commands:
          - "redis-server"
        shell: "/bin/sh"
      workloadCRDRef:
        apiVersion: "highlander.plexobject.io/v1"
        kind: "WorkloadPersistent"
        name: "crd-persistent"
        namespace: "default"
EOF

  # Make sed command compatible with both Linux and macOS
  # On macOS, sed requires an extension for the -i flag
  if [[ "$OSTYPE" == "darwin"* ]]; then
    # macOS version
    sed -i '' "s|ID_PLACEHOLDER|$id|g" /tmp/k8-highlander/config/config.yaml
    sed -i '' "s|PORT_PLACEHOLDER|$port|g" /tmp/k8-highlander/config/config.yaml
    sed -i '' "s|REDIS_ADDR_PLACEHOLDER|$redis_addr|g" /tmp/k8-highlander/config/config.yaml
    sed -i '' "s|CLUSTER_NAME_PLACEHOLDER|$cluster_name|g" /tmp/k8-highlander/config/config.yaml
    sed -i '' "s|KUBECONFIG_PLACEHOLDER|$kubeconfig|g" /tmp/k8-highlander/config/config.yaml
  else
    # Linux version
    sed -i "s|ID_PLACEHOLDER|$id|g" /tmp/k8-highlander/config/config.yaml
    sed -i "s|PORT_PLACEHOLDER|$port|g" /tmp/k8-highlander/config/config.yaml
    sed -i "s|REDIS_ADDR_PLACEHOLDER|$redis_addr|g" /tmp/k8-highlander/config/config.yaml
    sed -i "s|CLUSTER_NAME_PLACEHOLDER|$cluster_name|g" /tmp/k8-highlander/config/config.yaml
    sed -i "s|KUBECONFIG_PLACEHOLDER|$kubeconfig|g" /tmp/k8-highlander/config/config.yaml
  fi

  info "Configuration file created at /tmp/k8-highlander/config/config.yaml"
}


# Check prerequisites
check_prerequisites() {
  # Check if kubectl is available
  if ! command_exists kubectl; then
    error "kubectl could not be found, please install it first"
    exit 1
  fi
}

# Main function
main() {
  local id=""
  local port=8080

  # Parse arguments
  if [ $# -gt 0 ]; then
    id=$1
  fi

  if [ $# -gt 1 ]; then
    port=$2
  fi

  # Run steps
  check_prerequisites
  create_directories

  # Capture function outputs with no logging mixed in
  redis_addr=$(setup_redis)
  kubeconfig=$(determine_kubeconfig)
  cluster_name=$(detect_cluster_name "$kubeconfig")

  # Now that we have the values, we can log information about them
  info "Using Redis address: $redis_addr"
  info "Using kubeconfig: $kubeconfig"
  info "Using cluster name: $cluster_name"

  create_crd_definitions
  apply_crd_definitions

  create_crd_instances
  apply_crd_instances

  create_config_file "$id" "$port" "$redis_addr" "$kubeconfig" "$cluster_name"

  info "Setup complete. Configuration saved to /tmp/k8-highlander/config/config.yaml"
  info "CRDs and instances created in the cluster"
  info "Run your leader controller with this configuration to test CRD-based workloads"
}

# Execute main with all arguments
main "$@"
