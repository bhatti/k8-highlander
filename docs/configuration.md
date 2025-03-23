# Configuration Reference

The K8 Highlander can be configured using a YAML configuration file, environment variables, and command-line 
flags. This document describes all available configuration options.

## Configuration Methods

Configuration options are applied in the following order of precedence (highest to lowest):

1. Command-line flags
2. Environment variables
3. Configuration file
4. Default values

## Configuration File

The configuration file is a YAML file that can be specified using the `--config` flag. By default, the controller looks for a configuration file at `/etc/k8-highlander/config.yaml`.

### Example Configuration File

```yaml
# Basic configuration
id: "controller-1"
tenant: "default"
port: 8080
namespace: "default"

# Storage configuration
storageType: "redis"  # "redis" or "db"
redis:
  addr: "redis-host:6379"
  password: "password"
  db: 0
databaseURL: "postgres://user:password@postgres-host:5432/stateful?sslmode=disable"

# Cluster configuration
cluster:
  name: "primary"
  kubeconfig: "/path/to/kubeconfig"

# Workloads configuration
workloads:
  processes:
    - name: "example-process"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Starting example process'"
          - "sleep 3600"
        shell: "/bin/sh"
      env:
        EXAMPLE_VAR: "example-value"
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"
      restartPolicy: "OnFailure"

  cronJobs:
    - name: "example-cronjob"
      schedule: "*/15 * * * *"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Running example cron job at $(date)'"
        shell: "/bin/sh"
      restartPolicy: "OnFailure"

  services:
    - name: "example-service"
      image: "nginx:alpine"
      replicas: 1
      ports:
        - name: "http"
          containerPort: 80
          servicePort: 8080
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"

  persistentSets:
    - name: "example-persistent"
      image: "postgres:14-alpine"
      replicas: 1
      env:
        POSTGRES_PASSWORD: "example"
        POSTGRES_USER: "example"
        POSTGRES_DB: "example"
      ports:
        - name: "postgres"
          containerPort: 5432
          servicePort: 5432
      persistentVolumes:
        - name: "data"
          mountPath: "/var/lib/postgresql/data"
          size: "1Gi"
      resources:
        cpuRequest: "200m"
        memoryRequest: "256Mi"
        cpuLimit: "500m"
        memoryLimit: "512Mi"
```

## Environment Variables

The following environment variables can be used to configure the controller:

| Variable | Description | Default |
|----------|-------------|---------|
| `HIGHLANDER_ID` | Controller ID | Hostname |
| `HIGHLANDER_TENANT` | Tenant name | `default` |
| `HIGHLANDER_PORT` | HTTP server port | `8080` |
| `HIGHLANDER_NAMESPACE` | Kubernetes namespace | `default` |
| `HIGHLANDER_STORAGE_TYPE` | Storage type (`redis` or `db`) | `redis` |
| `HIGHLANDER_REDIS_ADDR` | Redis server address | `localhost:6379` |
| `HIGHLANDER_REDIS_PASSWORD` | Redis server password | `""` |
| `HIGHLANDER_REDIS_DB` | Redis database number | `0` |
| `HIGHLANDER_DATABASE_URL` | Database connection URL | `""` |
| `HIGHLANDER_KUBECONFIG` | Path to kubeconfig file | `""` |
| `HIGHLANDER_CLUSTER_NAME` | Cluster name | `default` |
| `HIGHLANDER_LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |

## Command-Line Flags

The following command-line flags can be used to configure the controller:

| Flag | Description | Default |
|------|-------------|---------|
| `--config` | Path to configuration file | `/etc/k8-highlander/config.yaml` |
| `--id` | Controller ID | Hostname |
| `--tenant` | Tenant name | `default` |
| `--port` | HTTP server port | `8080` |
| `--namespace` | Kubernetes namespace | `default` |
| `--storage-type` | Storage type (`redis` or `db`) | `redis` |
| `--redis-addr` | Redis server address | `localhost:6379` |
| `--redis-password` | Redis server password | `""` |
| `--redis-db` | Redis database number | `0` |
| `--database-url` | Database connection URL | `""` |
| `--kubeconfig` | Path to kubeconfig file | `""` |
| `--cluster-name` | Cluster name | `default` |
| `--log-level` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `--version` | Show version information | `false` |

## Configuration Options

### Basic Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `id` | Controller ID | Hostname |
| `tenant` | Tenant name | `default` |
| `port` | HTTP server port | `8080` |
| `namespace` | Kubernetes namespace | `default` |

### Storage Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `storageType` | Storage type (`redis` or `db`) | `redis` |
| `redis.addr` | Redis server address | `localhost:6379` |
| `redis.password` | Redis server password | `""` |
| `redis.db` | Redis database number | `0` |
| `databaseURL` | Database connection URL | `""` |

### Cluster Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `cluster.name` | Cluster name | `default` |
| `cluster.kubeconfig` | Path to kubeconfig file | `""` |

### Workload Configuration

#### Common Workload Options

The following options are available for all workload types:

| Option | Description | Default |
|--------|-------------|---------|
| `name` | Workload name | Required |
| `namespace` | Kubernetes namespace | Value from controller config |
| `image` | Container image | Required |
| `env` | Environment variables | `{}` |
| `resources` | Resource requirements | See below |
| `annotations` | Kubernetes annotations | `{}` |
| `labels` | Kubernetes labels | `{}` |
| `nodeSelector` | Node selector | `{}` |
| `configMaps` | ConfigMap volumes | `[]` |
| `secrets` | Secret volumes | `[]` |

#### Resource Requirements

| Option | Description | Default |
|--------|-------------|---------|
| `resources.cpuRequest` | CPU request | `""` |
| `resources.memoryRequest` | Memory request | `""` |
| `resources.cpuLimit` | CPU limit | `""` |
| `resources.memoryLimit` | Memory limit | `""` |

#### Script Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `script.commands` | List of commands to run | `[]` |
| `script.shell` | Shell to use | `/bin/sh` |

#### Process Workload Options

| Option | Description | Default |
|--------|-------------|---------|
| `restartPolicy` | Kubernetes restart policy | `OnFailure` |
| `terminationGracePeriod` | Termination grace period | `30s` |
| `maxRestarts` | Maximum number of restarts | `5` |
| `sidecars` | Sidecar containers | `[]` |

#### CronJob Workload Options

| Option | Description | Default |
|--------|-------------|---------|
| `schedule` | Cron schedule | Required |
| `restartPolicy` | Kubernetes restart policy | `OnFailure` |

#### Service Workload Options
| Option | Description | Default |
|--------|-------------|---------|
| `replicas` | Number of replicas | `1` |
| `ports` | Container ports | `[]` |
| `healthCheckPath` | Health check path | `""` |
| `healthCheckPort` | Health check port | `0` |
| `readinessTimeout` | Readiness timeout | `5m` |
| `terminationGracePeriod` | Termination grace period | `30s` |

#### Persistent Workload Options

| Option | Description | Default |
|--------|-------------|---------|
| `replicas` | Number of replicas | `1` |
| `ports` | Container ports | `[]` |
| `healthCheckPath` | Health check path | `""` |
| `healthCheckPort` | Health check port | `0` |
| `readinessTimeout` | Readiness timeout | `5m` |
| `serviceName` | Service name | Same as workload name |
| `persistentVolumes` | Persistent volumes | `[]` |

#### Port Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `name` | Port name | Required |
| `containerPort` | Container port | Required |
| `servicePort` | Service port | Same as container port |

#### Persistent Volume Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `name` | Volume name | Required |
| `mountPath` | Mount path | Required |
| `storageClassName` | Storage class name | `""` |
| `size` | Volume size | Required |

#### Sidecar Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `name` | Sidecar name | Required |
| `image` | Container image | Required |
| `command` | Container command | `[]` |
| `args` | Container arguments | `[]` |
| `env` | Environment variables | `{}` |
| `resources` | Resource requirements | See above |
| `volumeMounts` | Volume mounts | `[]` |
| `securityContext` | Security context | `{}` |

## Advanced Configuration

### Leader Election

The leader election process can be tuned using the following environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `HIGHLANDER_LEADER_LOCK_TTL` | Leader lock TTL in seconds | `15` |
| `HIGHLANDER_LEADER_RENEW_INTERVAL` | Leader lock renewal interval in seconds | `5` |

### Monitoring

The monitoring server can be configured using the following environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `HIGHLANDER_METRICS_PORT` | Metrics server port | Same as `port` |
| `HIGHLANDER_METRICS_PATH` | Metrics path | `/metrics` |

### Logging

Logging can be configured using the following environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `HIGHLANDER_LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `HIGHLANDER_LOG_FORMAT` | Log format (`text`, `json`) | `text` |

## Configuration Examples

### Basic Configuration

```yaml
id: "controller-1"
tenant: "default"
port: 8080
namespace: "default"

storageType: "redis"
redis:
  addr: "redis:6379"

cluster:
  name: "primary"
```

### Multi-Tenant Configuration

```yaml
# Controller 1 (Tenant A)
id: "controller-1"
tenant: "tenant-a"
namespace: "tenant-a"

storageType: "redis"
redis:
  addr: "redis:6379"

cluster:
  name: "primary"
```

```yaml
# Controller 2 (Tenant B)
id: "controller-2"
tenant: "tenant-b"
namespace: "tenant-b"

storageType: "redis"
redis:
  addr: "redis:6379"

cluster:
  name: "primary"
```

### High Availability Configuration

```yaml
id: ""  # Will use hostname
tenant: "production"
namespace: "production"

storageType: "redis"
redis:
  addr: "redis-ha:6379"
  password: "password"

cluster:
  name: "primary"
```

### Database Storage Configuration

```yaml
id: "controller-1"
tenant: "default"
namespace: "default"

storageType: "db"
databaseURL: "postgres://user:password@postgres:5432/stateful?sslmode=disable"

cluster:
  name: "primary"
```

### Process Workload Example

```yaml
workloads:
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
        DB_USER: "processor"
        DB_PASSWORD: "password"
      resources:
        cpuRequest: "200m"
        memoryRequest: "256Mi"
        cpuLimit: "500m"
        memoryLimit: "512Mi"
      restartPolicy: "OnFailure"
      terminationGracePeriod: "30s"
      sidecars:
        - name: "metrics"
          image: "prom/statsd-exporter:latest"
          resources:
            cpuRequest: "50m"
            memoryRequest: "64Mi"
```

### CronJob Workload Example

```yaml
workloads:
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
        OUTPUT_DIR: "/reports"
      resources:
        cpuRequest: "100m"
        memoryRequest: "128Mi"
        cpuLimit: "200m"
        memoryLimit: "256Mi"
      restartPolicy: "OnFailure"
      configMaps:
        - "report-config"
```

### Service Workload Example

```yaml
workloads:
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
        DB_HOST: "postgres.example.com"
      resources:
        cpuRequest: "100m"
        memoryRequest: "128Mi"
        cpuLimit: "200m"
        memoryLimit: "256Mi"
      healthCheckPath: "/health"
      healthCheckPort: 8080
      readinessTimeout: "2m"
```

### Persistent Workload Example

```yaml
workloads:
  persistentSets:
    - name: "message-queue"
      image: "ibmcom/mqadvanced-server:latest"
      replicas: 1
      ports:
        - name: "mq"
          containerPort: 1414
          servicePort: 1414
        - name: "web"
          containerPort: 9443
          servicePort: 9443
      persistentVolumes:
        - name: "data"
          mountPath: "/var/mqm"
          size: "10Gi"
          storageClassName: "standard"
      env:
        LICENSE: "accept"
        MQ_QMGR_NAME: "QM1"
      resources:
        cpuRequest: "500m"
        memoryRequest: "1Gi"
        cpuLimit: "1"
        memoryLimit: "2Gi"
      healthCheckPath: "/health"
      healthCheckPort: 9443
```

## Configuration Validation

The K8 Highlander validates the configuration at startup and will fail to start if the configuration is invalid. The following validation rules are applied:

- Required fields must be present
- Field values must be of the correct type
- Field values must be within valid ranges
- References to Kubernetes resources must be valid

If the configuration is invalid, the controller will log an error message and exit with a non-zero status code.

## Configuration Reloading

The K8 Highlander does not currently support dynamic configuration reloading. To change the configuration, you must restart the controller.

## Secrets Management

Sensitive configuration values (e.g., passwords, API keys) can be provided using environment variables or Kubernetes secrets. It is recommended to use Kubernetes secrets for sensitive values in production environments.

Example using Kubernetes secrets:

```yaml
# Create a secret
apiVersion: v1
kind: Secret
metadata:
  name: k8-highlander-secrets
  namespace: default
type: Opaque
data:
  redis-password: cGFzc3dvcmQ=  # base64-encoded "password"
```

```yaml
# Reference the secret in the environment variables
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
spec:
  template:
    spec:
      containers:
      - name: controller
        env:
        - name: HIGHLANDER_REDIS_PASSWORD
          valueFrom:
            secretKeyRef:
              name: k8-highlander-secrets
              key: redis-password
```

