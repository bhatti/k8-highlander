# Metrics Reference

The K8 Highlander exposes Prometheus metrics that can be used to monitor the health and performance of the 
controller and its workloads. This document describes the available metrics and how to use them.

## Metrics Endpoint

Metrics are exposed at the `/metrics` endpoint of the controller's HTTP server, 
which defaults to `http://localhost:8080/metrics`.

## Metric Types

The K8 Highlander exposes the following types of metrics:

- **Gauges**: Represent a single numerical value that can go up and down
- **Counters**: Represent a cumulative value that can only increase
- **Histograms**: Represent a distribution of values

## Available Metrics

### Leadership Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `k8_highlander_is_leader` | Gauge | Indicates if this instance is currently the leader (1) or not (0) | None |
| `k8_highlander_leadership_transitions_total` | Counter | Total number of leadership transitions | None |
| `k8_highlander_leadership_duration_seconds` | Histogram | Duration of leadership periods | None |
| `k8_highlander_leadership_acquisitions_total` | Counter | Total number of leadership acquisitions | None |
| `k8_highlander_leadership_losses_total` | Counter | Total number of leadership losses | None |
| `k8_highlander_leadership_failed_attempts_total` | Counter | Total number of failed leadership acquisition attempts | None |
| `k8_highlander_last_leader_transition_timestamp_seconds` | Gauge | Timestamp of the last leadership transition | None |

### Workload Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `k8_highlander_workload_status` | Gauge | Status of managed workloads (1=active, 0=inactive) | `type`, `name`, `namespace` |
| `k8_highlander_workload_startup_duration_seconds` | Histogram | Time taken to start workloads | `type`, `name` |
| `k8_highlander_workload_shutdown_duration_seconds` | Histogram | Time taken to shut down workloads | `type`, `name` |
| `k8_highlander_workload_operations_total` | Counter | Total number of operations performed on workloads | `type`, `name`, `operation` |
| `k8_highlander_workload_errors_total` | Counter | Total number of errors encountered with workloads | `type`, `name`, `error_type` |

### Failover Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `k8_highlander_failover_duration_seconds` | Histogram | Time taken to complete a failover | None |
| `k8_highlander_failover_total` | Counter | Total number of failovers | None |
| `k8_highlander_failover_errors_total` | Counter | Total number of failover errors | None |

### Redis Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `k8_highlander_redis_operations_total` | Counter | Total number of Redis operations | `operation` |
| `k8_highlander_redis_errors_total` | Counter | Total number of Redis errors | `operation`, `error_type` |
| `k8_highlander_redis_latency_seconds` | Histogram | Latency of Redis operations | `operation` |
| `k8_highlander_redis_lock_ttl_seconds` | Gauge | TTL of the Redis lock in seconds | None |

### Cluster Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `k8_highlander_cluster_health` | Gauge | Health status of clusters (1=healthy, 0=unhealthy) | `cluster` |
| `k8_highlander_cluster_priority` | Gauge | Priority of clusters (lower is higher priority) | `cluster` |
| `k8_highlander_cluster_transitions_total` | Counter | Total number of transitions between clusters | `from_cluster`, `to_cluster` |

### System Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `k8_highlander_system_load` | Gauge | System load average | None |
| `k8_highlander_goroutine_count` | Gauge | Number of goroutines | None |
| `k8_highlander_memory_usage_bytes` | Gauge | Memory usage in bytes | None |

### Workload-Specific Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `k8_highlander_process_status` | Gauge | Status of managed process jobs (1=active, 0=suspended) | `name`, `namespace` |
| `k8_highlander_service_status` | Gauge | Status of managed service jobs (1=active, 0=suspended) | `name`, `namespace` |
| `k8_highlander_persistent_status` | Gauge | Status of managed persistent jobs (1=active, 0=suspended) | `name`, `namespace` |
| `k8_highlander_cronjob_status` | Gauge | Status of managed cron jobs (1=active, 0=suspended) | `name`, `namespace` |

## Metric Labels

The following labels are used in the metrics:

| Label | Description | Example |
|-------|-------------|---------|
| `type` | Type of workload | `process`, `cronjob`, `service`, `persistent` |
| `name` | Name of workload | `example-process` |
| `namespace` | Kubernetes namespace | `default` |
| `operation` | Type of operation | `startup`, `shutdown`, `get`, `setNX`, `expire`, `eval` |
| `error_type` | Type of error | `timeout`, `connection_refused`, `not_found` |
| `cluster` | Name of cluster | `primary` |
| `from_cluster` | Source cluster in a transition | `primary` |
| `to_cluster` | Destination cluster in a transition | `secondary` |

## Prometheus Configuration

To scrape metrics from the K8 Highlander, add the following job to your Prometheus configuration:

```yaml
scrape_configs:
  - job_name: 'k8-highlander'
    scrape_interval: 15s
    static_configs:
      - targets: ['k8-highlander:8080']
```

If you're using the Prometheus Operator, you can create a ServiceMonitor:

```yaml
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

## Grafana Dashboards

A sample Grafana dashboard for the K8 Highlander is available in the `dashboards` directory of the repository. 
You can import this dashboard into your Grafana instance to visualize the metrics.

### Dashboard Panels

The dashboard includes the following panels:

1. **Leadership Status**: Shows the current leader and leadership transitions
2. **Workload Status**: Shows the status of all workloads
3. **Redis Operations**: Shows the rate of Redis operations and errors
4. **Failover Events**: Shows failover events and their duration
5. **System Resources**: Shows system resource usage (CPU, memory, goroutines)

## Alerting Rules

The following Prometheus alerting rules are recommended for monitoring the K8 Highlander:

```yaml
groups:
- name: k8-highlander
  rules:
  - alert: NoLeader
    expr: sum(k8_highlander_is_leader) == 0
    for: 1m
    labels:
      severity: critical
    annotations:
      summary: "No leader elected"
      description: "No leader has been elected for the K8 Highlander for more than 1 minute."

  - alert: FrequentLeadershipTransitions
    expr: rate(k8_highlander_leadership_transitions_total[5m]) > 0.1
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "Frequent leadership transitions"
      description: "The K8 Highlander is experiencing frequent leadership transitions (more than 1 every 10 minutes)."

  - alert: HighFailoverDuration
    expr: k8_highlander_failover_duration_seconds > 30
    labels:
      severity: warning
    annotations:
      summary: "High failover duration"
      description: "The K8 Highlander failover took more than 30 seconds to complete."

  - alert: WorkloadNotHealthy
    expr: k8_highlander_workload_status == 1 and on(name, namespace) (sum by(name, namespace) (k8_highlander_workload_errors_total) > 0)
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "Workload not healthy"
      description: "The workload {{ $labels.name }} in namespace {{ $labels.namespace }} is active but has errors."

  - alert: RedisConnectionIssues
    expr: rate(k8_highlander_redis_errors_total[5m]) > 0
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "Redis connection issues"
      description: "The K8 Highlander is experiencing Redis connection issues."
```

## Metric Retention and Aggregation

Prometheus metrics are time-series data and can grow in size over time. It is recommended to configure appropriate retention and aggregation policies in Prometheus to manage the growth of metrics data.

Example Prometheus configuration for metric retention:

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s
  # How long to retain metrics
  retention: 15d

# Storage configuration
storage:
  tsdb:
    # How long to retain metrics
    retention.time: 15d
    # Maximum size of the TSDB blocks directory
    retention.size: 5GB
    # Minimum age of a block before it can be compacted
    min_block_duration: 2h
    # Maximum age of a block before it must be compacted
    max_block_duration: 6h
```

## Custom Metrics

The K8 Highlander does not currently support adding custom metrics. If you need to add custom metrics, you can modify the source code to add them.

## Troubleshooting

If you're having trouble with metrics, check the following:

1. Ensure the controller is running and the HTTP server is accessible
2. Check that the `/metrics` endpoint is returning data
3. Verify that Prometheus is configured to scrape the controller
4. Check for any errors in the controller logs related to metrics

Common issues:

- **No metrics data**: Ensure the controller is running and the HTTP server is accessible
- **Missing metrics**: Check that the controller is configured correctly and the workloads are running
- **Stale metrics**: Check that Prometheus is scraping the controller at the expected interval

