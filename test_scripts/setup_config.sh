#!/bin/bash -ex

set -e

# Create directories
mkdir -p /tmp/k8-highlander/config
mkdir -p /tmp/k8-highlander/logs
mkdir -p /tmp/k8-highlander/pids

# Check if we're running in GCP
REDIS_ADDR="localhost:6379"
if command -v gcloud &> /dev/null; then
    REDIS_IP=$(gcloud redis instances describe leader-election --region=us-central1 --format='value(host)' 2>/dev/null)
    if [ ! -z "$REDIS_IP" ]; then
        echo "GCP Redis instance detected at: $REDIS_IP"
        REDIS_ADDR="$REDIS_IP:6379"
    fi
else
    # Start Redis if not already running
    if ! docker ps | grep -q redis; then
        echo "Starting Redis container..."
        docker run -d --name redis-stack-server -p 6379:6379 redis/redis-stack-server:latest
        sleep 2
    fi
fi

# Determine kubeconfig path
KUBECONFIG="$HOME/.kube/config"
if [ -f "./primary-kubeconfig.yaml" ]; then
    KUBECONFIG="$(pwd)/primary-kubeconfig.yaml"
    echo "Using primary kubeconfig: $KUBECONFIG"
fi

ID=""
if [ $# -gt 0  ];
then
    ID=$1
fi;
PORT=8080
if [ $# -gt 1  ];
then
    PORT=$2
fi;
# Create a basic configuration file
cat > /tmp/k8-highlander/config/config.yaml << EOF
id: "$ID"
tenant: "test-tenant"
port: $PORT
namespace: "default"

# Storage configuration
storageType: "redis"
redis:
  addr: "$REDIS_ADDR"
  password: ""
  db: 0

# Cluster configuration
cluster:
  name: "default"
  kubeconfig: "$KUBECONFIG"

# Workloads configuration
workloads:
  processes:
    - name: "singleton-echo-1"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Starting singleton process'"
          - "echo 'Process ID: $$'"
          - "while true; do echo \"Singleton #1 heartbeat at $(date)\"; sleep 10; done"
        shell: "/bin/sh"
      env:
        TEST_ENV: "test-value"
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"
      restartPolicy: "Never"
    - name: "singleton-echo-2"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Starting singleton process'"
          - "echo 'Process ID: $$'"
          - "while true; do echo \"Singleton #2 heartbeat at $(date)\"; sleep 10; done"
        shell: "/bin/sh"
      env:
        TEST_ENV: "test-value"
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"
      restartPolicy: "Never"

  cronJobs:
    - name: "singleton-cron-1"
      schedule: "*/1 * * * *"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Running cron job #1 at $(date)'"
          - "echo 'Hostname: $(hostname)'"
          - sleep 15
        shell: "/bin/sh"
      env:
        TEST_ENV: "test-value"
      resources:
        cpuRequest: "50m"
        memoryRequest: "32Mi"
        cpuLimit: "100m"
        memoryLimit: "64Mi"
      restartPolicy: "OnFailure"
    - name: "singleton-cron-2"
      schedule: "*/1 * * * *"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Running cron job #2 at $(date)'"
          - "echo 'Hostname: $(hostname)'"
          - sleep 15
        shell: "/bin/sh"
      env:
        TEST_ENV: "test-value"
      resources:
        cpuRequest: "50m"
        memoryRequest: "32Mi"
        cpuLimit: "100m"
        memoryLimit: "64Mi"
      restartPolicy: "OnFailure"

  services:
    - name: "singleton-service-1"
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
    - name: "singleton-service-2"
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

  persistentSets:
    - name: "singleton-persistent-1"
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
    - name: "singleton-persistent-2"
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
EOF

echo "Setup complete. Configuration saved to /tmp/k8-highlander/config/config.yaml"
