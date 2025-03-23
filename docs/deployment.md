# Deployment Strategies

This document describes various strategies for deploying the K8 Highlander in different 
environments and scenarios.

## Basic Deployment

The simplest deployment strategy is to run a single instance of the K8 Highlander in a Kubernetes cluster. 
This is suitable for development and testing environments, but not recommended for production due to 
the lack of high availability.

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  replicas: 1
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
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis:6379"
        - name: HIGHLANDER_TENANT
          value: "default"
        - name: HIGHLANDER_NAMESPACE
          value: "default"
        ports:
        - containerPort: 8080
          name: http
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 256Mi
```

## High Availability Deployment

For production environments, it's recommended to run multiple instances of the K8 Highlander to 
ensure high availability. The leader election mechanism ensures that only one instance is active at a time, 
with the others ready to take over if the leader fails.

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  replicas: 3  # Run multiple instances for HA
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
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis:6379"
        - name: HIGHLANDER_TENANT
          value: "production"
        - name: HIGHLANDER_NAMESPACE
          value: "production"
        ports:
        - containerPort: 8080
          name: http
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: "1"
            memory: 512Mi
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 15
          periodSeconds: 20
```

### Redis High Availability

Since Redis is used for leader election, it's important to ensure that Redis is also highly available. 
There are several options for deploying Redis in a highly available configuration:

1. **Redis Sentinel**: Provides high availability with automatic failover
2. **Redis Cluster**: Provides high availability and sharding
3. **Managed Redis Service**: Cloud providers offer managed Redis services with high availability

Example Redis Sentinel deployment:

```yaml
# Redis master
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis-master
  namespace: k8-highlander
spec:
  serviceName: redis-master
  replicas: 1
  selector:
    matchLabels:
      app: redis-master
  template:
    metadata:
      labels:
        app: redis-master
    spec:
      containers:
      - name: redis
        image: redis:6.2
        ports:
        - containerPort: 6379
          name: redis
        volumeMounts:
        - name: data
          mountPath: /data
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: [ "ReadWriteOnce" ]
      resources:
        requests:
          storage: 1Gi

---
# Redis sentinel
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis-sentinel
  namespace: k8-highlander
spec:
  serviceName: redis-sentinel
  replicas: 3
  selector:
    matchLabels:
      app: redis-sentinel
  template:
    metadata:
      labels:
        app: redis-sentinel
    spec:
      containers:
      - name: sentinel
        image: redis:6.2
        command: ["redis-sentinel", "/etc/redis/sentinel.conf"]
        ports:
        - containerPort: 26379
          name: sentinel
        volumeMounts:
        - name: config
          mountPath: /etc/redis
      volumes:
      - name: config
        configMap:
          name: redis-sentinel-config
```

## Multi-Tenant Deployment

The K8 Highlander supports multi-tenant deployments, where different tenants/teams can have their own leader 
election and workload management. This is useful for organizations with multiple teams or environments.

### Separate Namespaces

Each tenant can have its own namespace with its own instances of the K8 Highlander:

```yaml
# Tenant A
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: tenant-a
spec:
  replicas: 2
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
          value: "tenant-a"
        - name: HIGHLANDER_NAMESPACE
          value: "tenant-a"
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis:6379"
```

```yaml
# Tenant B
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: tenant-b
spec:
  replicas: 2
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
          value: "tenant-b"
        - name: HIGHLANDER_NAMESPACE
          value: "tenant-b"
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis:6379"
```

### Shared Namespace

Alternatively, multiple tenants can share a namespace but have separate tenant IDs:

```yaml
# Tenant A
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander-tenant-a
  namespace: k8-highlander
spec:
  replicas: 2
  selector:
    matchLabels:
      app: k8-highlander
      tenant: tenant-a
  template:
    metadata:
      labels:
        app: k8-highlander
        tenant: tenant-a
    spec:
      containers:
      - name: controller
        image: plexobject/k8-highlander:latest
        env:
        - name: HIGHLANDER_TENANT
          value: "tenant-a"
        - name: HIGHLANDER_NAMESPACE
          value: "tenant-a"
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis:6379"
```

```yaml
# Tenant B
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander-tenant-b
  namespace: k8-highlander
spec:
  replicas: 2
  selector:
    matchLabels:
      app: k8-highlander
      tenant: tenant-b
  template:
    metadata:
      labels:
        app: k8-highlander
        tenant: tenant-b
    spec:
      containers:
      - name: controller
        image: plexobject/k8-highlander:latest
        env:
        - name: HIGHLANDER_TENANT
          value: "tenant-b"
        - name: HIGHLANDER_NAMESPACE
          value: "tenant-b"
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis:6379"
```

## Multi-Cluster Deployment

The K8 Highlander can be deployed across multiple Kubernetes clusters for even higher 
availability and disaster recovery.

### Active-Passive Configuration

In an active-passive configuration, one cluster is active and the other is on standby. If the active cluster fails, 
the standby cluster takes over.

```yaml
# Primary cluster
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  replicas: 2
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
        - name: HIGHLANDER_CLUSTER_NAME
          value: "primary"
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis.example.com:6379"  # External Redis accessible from both clusters
```

```yaml
# Secondary cluster
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  replicas: 2
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
        - name: HIGHLANDER_CLUSTER_NAME
          value: "secondary"
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis.example.com:6379"  # External Redis accessible from both clusters
```

## Helm Chart Deployment

The K8 Highlander can be deployed using Helm, which simplifies the deployment process and allows for easy configuration.

### Installing with Helm

```bash
# Add the Helm repository
helm repo add k8-highlander https://bhatti.github.io/k8-highlander-charts
helm repo update

# Install the chart
helm install k8-highlander k8-highlander/k8-highlander \
  --namespace k8-highlander \
  --create-namespace \
  --set redis.addr=redis:6379 \
  --set tenant=production \
  --set namespace=production \
  --set replicas=3
```

### Helm Chart Values

The Helm chart supports the following values:

```yaml
# values.yaml
# Basic configuration
id: ""  # Will use pod name if empty
tenant: "default"
namespace: "default"
port: 8080
replicas: 2

# Storage configuration
storageType: "redis"  # "redis" or "db"
redis:
  addr: "redis:6379"
  password: ""
  db: 0
databaseURL: ""  # Used if storageType is "db"

# Cluster configuration
cluster:
  name: "primary"
  kubeconfig: ""  # Will use in-cluster config if empty

# Resource configuration
resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: 1
    memory: 512Mi

# Workloads configuration
workloads:
  processes: []
  cronJobs: []
  services: []
  persistentSets: []

# Monitoring configuration
monitoring:
  enabled: true
  serviceMonitor:
    enabled: false
    interval: 15s

# Security configuration
securityContext:
  runAsUser: 1000
  runAsGroup: 1000
  fsGroup: 1000

# Pod configuration
podAnnotations: {}
podLabels: {}
nodeSelector: {}
tolerations: []
affinity: {}
```

## GitOps Deployment

The K8 Highlander can be deployed using GitOps tools like Flux or ArgoCD, which automate the deployment process 
based on changes to a Git repository.

### Flux Example
```yaml
# flux-system/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespace.yaml
  - redis.yaml
  - k8-highlander.yaml
```

```yaml
# flux-system/namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: k8-highlander
```

```yaml
# flux-system/redis.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
  namespace: k8-highlander
spec:
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
      - name: redis
        image: redis:6.2
        ports:
        - containerPort: 6379
---
apiVersion: v1
kind: Service
metadata:
  name: redis
  namespace: k8-highlander
spec:
  selector:
    app: redis
  ports:
  - port: 6379
    targetPort: 6379
```

```yaml
# flux-system/k8-highlander.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  replicas: 3
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
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis:6379"
        - name: HIGHLANDER_TENANT
          value: "production"
        - name: HIGHLANDER_NAMESPACE
          value: "production"
        ports:
        - containerPort: 8080
          name: http
---
apiVersion: v1
kind: Service
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  selector:
    app: k8-highlander
  ports:
  - port: 8080
    targetPort: 8080
    name: http
```

### ArgoCD Example

```yaml
# argocd/application.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: k8-highlander
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/bhatti/k8-highlander-gitops.git
    targetRevision: HEAD
    path: manifests
  destination:
    server: https://kubernetes.default.svc
    namespace: k8-highlander
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
    - CreateNamespace=true
```

## Monitoring and Observability

To ensure the health and performance of the K8 Highlander, it's recommended to set up monitoring and observability tools.

### Prometheus and Grafana

The K8 Highlander exposes Prometheus metrics at the `/metrics` endpoint. You can use Prometheus to scrape 
these metrics and Grafana to visualize them.

```yaml
# prometheus-servicemonitor.yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: k8-highlander
  namespace: monitoring
spec:
  selector:
    matchLabels:
      app: k8-highlander
  endpoints:
  - port: http
    path: /metrics
    interval: 15s
```

### Loki for Logs

You can use Loki to collect and query logs from the K8 Highlander.

```yaml
# promtail-configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: promtail-config
  namespace: monitoring
data:
  promtail.yaml: |
    server:
      http_listen_port: 9080
      grpc_listen_port: 0
    positions:
      filename: /tmp/positions.yaml
    clients:
      - url: http://loki:3100/loki/api/v1/push
    scrape_configs:
      - job_name: kubernetes-pods
        kubernetes_sd_configs:
          - role: pod
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_label_app]
            regex: k8-highlander
            action: keep
```

## Security Considerations

When deploying the K8 Highlander, consider the following security best practices:

### RBAC Configuration

The K8 Highlander needs permissions to manage workloads in Kubernetes. It's recommended to use the principle 
of least privilege and grant only the necessary permissions.

```yaml
# rbac.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: k8-highlander
  namespace: k8-highlander
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: k8-highlander
  namespace: k8-highlander
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["apps"]
  resources: ["deployments", "statefulsets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["services"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["batch"]
  resources: ["cronjobs", "jobs"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: k8-highlander
  namespace: k8-highlander
subjects:
- kind: ServiceAccount
  name: k8-highlander
  namespace: k8-highlander
roleRef:
  kind: Role
  name: k8-highlander
  apiGroup: rbac.authorization.k8s.io
```

### Network Policies

Use network policies to restrict traffic to and from the K8 Highlander.

```yaml
# network-policy.yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  podSelector:
    matchLabels:
      app: k8-highlander
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: prometheus
    ports:
    - protocol: TCP
      port: 8080
  egress:
  - to:
    - podSelector:
        matchLabels:
          app: redis
    ports:
    - protocol: TCP
      port: 6379
  - to:
    - namespaceSelector: {}
    ports:
    - protocol: TCP
      port: 443  # Kubernetes API
```

### Secrets Management

Use Kubernetes secrets to manage sensitive information like Redis passwords.

```yaml
# secrets.yaml
apiVersion: v1
kind: Secret
metadata:
  name: k8-highlander-secrets
  namespace: k8-highlander
type: Opaque
data:
  redis-password: cGFzc3dvcmQ=  # base64-encoded "password"
```

```yaml
# deployment.yaml (excerpt)
env:
- name: HIGHLANDER_REDIS_PASSWORD
  valueFrom:
    secretKeyRef:
      name: k8-highlander-secrets
      key: redis-password
```

## Resource Requirements

The resource requirements for the K8 Highlander depend on the number and complexity of workloads being 
managed. Here are some general guidelines:

### Small Deployment (< 10 workloads)

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi
```

### Medium Deployment (10-50 workloads)

```yaml
resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: 1
    memory: 512Mi
```

### Large Deployment (> 50 workloads)

```yaml
resources:
  requests:
    cpu: 500m
    memory: 512Mi
  limits:
    cpu: 2
    memory: 1Gi
```

## Upgrade Strategies

When upgrading the K8 Highlander, consider the following strategies:

### Rolling Update

The default Kubernetes deployment strategy is a rolling update, which gradually replaces old pods with new ones.

```yaml
spec:
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
      maxSurge: 1
```

### Blue-Green Deployment

In a blue-green deployment, you deploy a new version alongside the old version and switch traffic once the new 
version is ready.

```yaml
# blue deployment (current version)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander-blue
  namespace: k8-highlander
spec:
  replicas: 3
  selector:
    matchLabels:
      app: k8-highlander
      deployment: blue
  template:
    metadata:
      labels:
        app: k8-highlander
        deployment: blue
    spec:
      containers:
      - name: controller
        image: plexobject/k8-highlander:latest
```

```yaml
# green deployment (new version)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander-green
  namespace: k8-highlander
spec:
  replicas: 3
  selector:
    matchLabels:
      app: k8-highlander
      deployment: green
  template:
    metadata:
      labels:
        app: k8-highlander
        deployment: green
    spec:
      containers:
      - name: controller
        image: plexobject/k8-highlander:latest
```

```yaml
# service (switch between blue and green)
apiVersion: v1
kind: Service
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  selector:
    app: k8-highlander
    deployment: blue  # Switch to green when ready
  ports:
  - port: 8080
    targetPort: 8080
    name: http
```

## Disaster Recovery

To ensure data integrity and availability in case of a disaster, consider the following strategies:

### Backup and Restore

Regularly backup the Redis or database used for leader election. For Redis, you can use the `SAVE` or `BGSAVE` commands to create snapshots.

```bash
# Backup Redis
redis-cli -h redis.example.com SAVE
cp /var/lib/redis/dump.rdb /backups/redis-backup-$(date +%Y%m%d).rdb

# Restore Redis
cp /backups/redis-backup-20230501.rdb /var/lib/redis/dump.rdb
redis-cli -h redis.example.com SHUTDOWN
# Redis will load the dump.rdb file on startup
```

### Multi-Region Deployment

For critical applications, consider deploying the K8 Highlander in multiple regions with a global Redis or database service.

```yaml
# Region 1
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  replicas: 2
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
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis.global.example.com:6379"
        - name: HIGHLANDER_CLUSTER_NAME
          value: "region1"
```

```yaml
# Region 2
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8-highlander
  namespace: k8-highlander
spec:
  replicas: 2
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
        - name: HIGHLANDER_REDIS_ADDR
          value: "redis.global.example.com:6379"
        - name: HIGHLANDER_CLUSTER_NAME
          value: "region2"
```

## Summary

The K8 Highlander can be deployed in various ways depending on your requirements for high availability, 
multi-tenancy, and disaster recovery. By following the strategies outlined in this document, you can ensure a reliable and secure deployment of the K8 Highlander in 
your Kubernetes environment.

For more information, see the following resources:

- [Architecture Overview](architecture.md)
- [Configuration Reference](configuration.md)
- [API Reference](api-reference.md)
- [Metrics Reference](metrics.md)
