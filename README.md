# K8 Highlander for Kubernetes

[![Go Report Card](https://goreportcard.com/badge/github.com/bhatti/k8-highlander)](https://goreportcard.com/report/github.com/bhatti/k8-highlander)
[![License](https://img.shields.io/github/license/bhatti/k8-highlander)](https://github.com/bhatti/k8-highlander/blob/main/LICENSE)
[![Docker Pulls](https://img.shields.io/docker/pulls/plexobject/k8-highlander)](https://hub.docker.com/r/plexobject/k8-highlander)

K8 Highlander allows managing singleton or stateful processes in Kubernetes clusters where only one instance of a 
service should exist at any time. It provides leader election, automatic failover, and management of various 
workload types with reliability.

## 🌟 Features

- **Leader Election**: Ensures only one controller instance is active at a time
- **Automatic Failover**: Seamlessly transitions workloads when the active instance fails
- **Multiple Workload Types**: Supports processes, cron jobs, services, and stateful sets
- **Multi-Tenant Support**: Run multiple isolated controller groups with separate leadership
- **Monitoring Dashboard**: Real-time visibility into controller and workload status
- **Prometheus Metrics**: Comprehensive metrics for monitoring and alerting
- **Storage Options**: Supports Redis or relational database for leader state storage

## 🚀 Use Cases

K8 Highlander is designed for scenarios where you need to ensure only one instance of a 
process is running across your Kubernetes cluster:

### Process Workloads

- **Single-instance processes** like sequential ID generators
- **Data Capture** that should not have duplicate instances
- **Batch processors** that need exclusive access to resources
- **Legacy applications** that weren't designed for horizontal scaling

```yaml
processes:
  - name: "data-processor"
    image: "mycompany/data-processor:latest"
    script:
      commands:
        - "echo 'Starting data processor'"
        - "/app/process-data.sh"
      shell: "/bin/sh"
    env:
      DB_HOST: "postgres.example.com"
    resources:
      cpuRequest: "200m"
      memoryRequest: "256Mi"
    restartPolicy: "OnFailure"
```

### Cron Job Workloads

- **Scheduled tasks** that should run exactly once
- **Periodic cleanup jobs** that shouldn't overlap
- **Report generation** on a schedule
- **Data synchronization** tasks

```yaml
cronJobs:
  - name: "daily-report"
    schedule: "0 0 * * *"  # Daily at midnight
    image: "mycompany/report-generator:latest"
    script:
      commands:
        - "echo 'Generating daily report'"
        - "/app/generate-report.sh"
      shell: "/bin/sh"
    env:
      REPORT_TYPE: "daily"
    restartPolicy: "OnFailure"
```

### Service Workloads (Deployments)

- **API servers** that need to be singleton but highly available
- **Web interfaces** for admin tools
- **Job managers** that coordinate work
- **Middleware components** that require singleton behavior

```yaml
services:
  - name: "admin-api"
    image: "mycompany/admin-api:latest"
    replicas: 1
    ports:
      - name: "http"
        containerPort: 8080
        servicePort: 80
    env:
      LOG_LEVEL: "info"
    resources:
      cpuRequest: "100m"
      memoryRequest: "128Mi"
```

### Persistent Workloads (StatefulSets)

- **Databases** that should only have one primary instance
- **Message queues** like messaging servers that need stable network identity
- **Cache servers** with persistent storage
- **Stateful applications** requiring ordered, graceful scaling

```yaml
persistentSets:
  - name: "message-queue"
    image: "ibmcom/mqadvanced-server:latest"
    replicas: 1
    ports:
      - name: "mq"
        containerPort: 1414
        servicePort: 1414
    persistentVolumes:
      - name: "data"
        mountPath: "/var/mqm"
        size: "10Gi"
    env:
      LICENSE: "accept"
      MQ_QMGR_NAME: "QM1"
    resources:
      cpuRequest: "500m"
      memoryRequest: "1Gi"
```

## 📋 Installation

### Prerequisites

- Kubernetes cluster (v1.16+)
- Redis server or PostgreSQL database for leader state storage
- kubectl configured to access your cluster

### Using Helm

```bash
# Add the Helm repository
helm repo add k8-highlander https://bhatti.github.io/k8-highlander-charts
helm repo update

# Install the chart
helm install k8-highlander k8-highlander/k8-highlander \
  --namespace k8-highlander \
  --create-namespace \
  --set redis.addr=my-redis:6379
```

### Using kubectl

```bash
# Create namespace
kubectl create namespace k8-highlander

# Create config map
kubectl create configmap k8-highlander-config \
  --from-file=config.yaml=./config/config.yaml \
  -n k8-highlander

# Apply deployment
kubectl apply -f https://raw.githubusercontent.com/bhatti/k8-highlander/main/deploy/kubernetes/deployment.yaml
```

### Using Docker

```bash
docker run -d --name k8-highlander \
  -v $(pwd)/config.yaml:/etc/k8-highlander/config.yaml \
  -e HIGHLANDER_REDIS_ADDR=redis-host:6379 \
  -p 8080:8080 \
  plexobject/k8-highlander:latest
```

## 🔧 Configuration

K8 Highlander can be configured using a YAML configuration file and/or environment variables.

### Configuration File

```yaml
# config.yaml
id: ""  # Will use hostname if empty
tenant: "default"
port: 8080
namespace: "default"

# Storage configuration
storageType: "redis"  # "redis" or "db"
redis:
  addr: "redis-host:6379"
  password: ""
  db: 0
databaseURL: ""  # Used if storageType is "db"

# Cluster configuration
cluster:
  name: "primary"
  kubeconfig: ""  # Will use in-cluster config if empty

# Workloads configuration
workloads:
  processes:
    - name: "example-process"
      # ... process configuration ...

  cronJobs:
    - name: "example-cronjob"
      # ... cron job configuration ...

  services:
    - name: "example-service"
      # ... service configuration ...

  persistentSets:
    - name: "example-persistent"
      # ... persistent set configuration ...
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `HIGHLANDER_ID` | Controller ID | Hostname |
| `HIGHLANDER_TENANT` | Tenant name | `default` |
| `HIGHLANDER_PORT` | HTTP server port | `8080` |
| `HIGHLANDER_STORAGE_TYPE` | Storage type (`redis` or `db`) | `redis` |
| `HIGHLANDER_REDIS_ADDR` | Redis server address | `localhost:6379` |
| `HIGHLANDER_REDIS_PASSWORD` | Redis server password | `""` |
| `HIGHLANDER_REDIS_DB` | Redis database number | `0` |
| `HIGHLANDER_DATABASE_URL` | Database connection URL | `""` |
| `HIGHLANDER_KUBECONFIG` | Path to kubeconfig file | `""` |
| `HIGHLANDER_CLUSTER_NAME` | Cluster name | `default` |
| `HIGHLANDER_NAMESPACE` | Kubernetes namespace | `default` |

## 📊 Monitoring

K8 Highlander provides a built-in dashboard and Prometheus metrics for monitoring.

### Dashboard

Access the dashboard at `http://<controller-address>:8080/`

![Leader Dashboard Screenshot](docs/images/dashboard-leader.png)
![Follower Dashboard Screenshot](docs/images/dashboard-follower.png)

### Prometheus Metrics

Metrics are exposed at `http://<controller-address>:8080/metrics`

Key metrics include:

- `k8_highlander_is_leader`: Indicates if this instance is the leader (1) or not (0)
- `k8_highlander_leadership_transitions_total`: Total number of leadership transitions
- `k8_highlander_workload_status`: Status of managed workloads (1=active, 0=inactive)
- `k8_highlander_failover_total`: Total number of failovers
- `k8_highlander_redis_operations_total`: Total number of Redis operations

## 🔍 Troubleshooting

### Common Issues

#### Controller Not Becoming Leader

- Check Redis connectivity: `redis-cli -h <redis-host> ping`
- Verify Redis keys: `redis-cli -h <redis-host> keys "highlander-leader-*"`
- Check controller logs: `kubectl logs -l app=k8-highlander -n k8-highlander`

#### Workloads Not Starting

- Check controller logs for errors
- Verify RBAC permissions: The controller needs permissions to create/update/delete pods, deployments, etc.
- Check workload configuration for errors

#### Failover Not Working

- Ensure multiple controller instances are running
- Check Redis connectivity from all instances
- Verify leader lock TTL settings (default: 15s)

### Debugging

Enable debug logging by setting the environment variable:

```bash
HIGHLANDER_LOG_LEVEL=debug
```

Access detailed status information:

```bash
curl http://<controller-address>:8080/debug/status
```

## 🧪 Examples

### Basic Setup with Process Workload

```yaml
# config.yaml
id: "controller-1"
tenant: "default"
namespace: "default"

redis:
  addr: "redis:6379"

workloads:
  processes:
    - name: "singleton-processor"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Starting singleton processor'"
          - "while true; do echo 'Processing...'; sleep 60; done"
        shell: "/bin/sh"
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
      restartPolicy: "Never"
```

### Multi-Tenant Setup

```yaml
# controller-1.yaml
id: "controller-1"
tenant: "tenant-a"  # First tenant
namespace: "tenant-a"

redis:
  addr: "redis:6379"

workloads:
  processes:
    - name: "tenant-a-processor"
      # ... configuration ...
```

```yaml
# controller-2.yaml
id: "controller-2"
tenant: "tenant-b"  # Second tenant
namespace: "tenant-b"

redis:
  addr: "redis:6379"

workloads:
  processes:
    - name: "tenant-b-processor"
      # ... configuration ...
```

### High Availability Setup

```yaml
# Deploy multiple instances with the same tenant
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  replicas: 2  # Run two instances for HA
  selector:
    matchLabels:
      app: k8-highlander
  template:
    metadata:
      labels:
        app: k8-highlander
    spec:
      containers:
      - name: controller
        image: plexobject/k8-highlander:latest
        env:
        - name: HIGHLANDER_TENANT
          value: "production"
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis:6379"
        # ... other configuration ...
```



# k8-highlander CRD Integration Guide

You can use the Custom Resource Definition (CRD) support in k8-highlander to manage workload configurations as well. This approach allows you to define workload configurations using Kubernetes native resources and reference them from your k8-highlander configuration.

## Step 1: Install the CRDs

First, apply the CRD definitions to your Kubernetes cluster:

```bash
kubectl apply -f config/highlander-crds.yaml
```

## Step 2: Create Workload Resources

Create some Custom Resources for your workloads similar to sample file:

```bash
kubectl apply -f config/sample-workload-resources.yaml
```

## Step 3: Reference CRDs in Your Configuration

Update your k8-highlander configuration to reference the Custom Resources:

```yaml
workloads:
  processes:
    - name: "process-name"
      workloadCRDRef:
        apiVersion: "highlander.plexobject.io/v1"
        kind: "WorkloadProcess"
        name: "singleton-process"
        namespace: "default"
```

## Step 4: Start k8-highlander with CRD Support

Start k8-highlander with the CRD support enabled:

```bash
k8-highlander --config=config.yaml
```

## Step 5: Check the status:

```bash
kubectl get workloadprocesses
```

You'll see the status reflecting the current state of the workload:

```
NAME               ACTIVE   HEALTHY   AGE
singleton-process  true     true      5m
```

## Troubleshooting

If you encounter issues with the CRD integration, check the following:

1. **CRDs are installed**: Verify that the CRDs are properly installed in your cluster.
   ```bash
   kubectl get crd | grep highlander
   ```

2. **Custom Resources exist**: Make sure the Custom Resources exist in the expected namespace.
   ```bash
   kubectl get workloadprocesses -n your-namespace
   ```

3. **CRD references are correct**: Check that the references in your configuration match the actual Custom Resources.

4. **k8-highlander logs**: Check the k8-highlander logs for any errors related to CRD loading.
   ```bash
   kubectl logs -n your-namespace deployment/k8-highlander
   ```

5. **Permissions**: Ensure that k8-highlander has the necessary permissions to read and update the Custom Resources.


## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📜 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🌟 Why K8 Highlander?

### The Problem

Kubernetes is designed for stateless applications that can scale horizontally. 
However, many real-world applications require singleton behavior where exactly one instance should 
be running at any time. Examples include:

- Legacy applications not designed for horizontal scaling
- Processes that need exclusive access to resources
- Scheduled jobs that should run exactly once
- Primary/replica database setups where only one primary should exist

Traditional solutions like Kubernetes StatefulSets provide stable network identities but don't solve 
the "exactly-one-active" problem across multiple nodes or clusters.

### The Solution

K8 Highlander provides:

1. **Leader Election**: Ensures only one controller is active at a time
2. **Automatic Failover**: If the active controller fails, another takes over
3. **Workload Management**: Manages different types of workloads with singleton behavior
4. **Multi-Tenant Support**: Run multiple isolated controller groups
5. **Monitoring and Metrics**: Real-time visibility into controller and workload status

By handling the complexity of leader election and workload management, K8 Highlander allows you to 
run stateful workloads in Kubernetes with confidence, knowing that exactly one instance will be active at any time.

---

## 📚 Further Reading

- [Architecture Overview](docs/architecture.md)
- [API Reference](docs/api-reference.md)
- [Configuration Reference](docs/configuration.md)
- [Metrics Reference](docs/metrics.md)
- [Deployment Strategies](docs/deployment.md)

---

Sponsored by [PlexObject Solutions, Inc.](https://plexobject.com) and made with ❤️ for the Kubernetes community.
