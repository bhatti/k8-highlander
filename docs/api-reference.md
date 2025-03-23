# API Reference

The K8 Highlander provides several HTTP endpoints for monitoring, management, and integration. This document 
describes the available endpoints and their usage.

## Base URL

All API endpoints are available at the controller's HTTP server address, which defaults to `http://localhost:8080`.

## Health Endpoints

### GET /healthz

Liveness probe endpoint. Returns a 200 OK response if the controller is running.

**Response:**
```
OK
```

### GET /readyz

Readiness probe endpoint. Returns a 200 OK response if the controller is ready to serve traffic, 
or a 503 Service Unavailable response if not.

**Response (Ready):**
```
OK
```

**Response (Not Ready):**
```
not ready - <reason>
```

Where `<reason>` can be one of:
- `recent leadership change`
- `redis connection issue`
- `cluster health issue`
- `error: <error message>`

## Status Endpoints

### GET /status

Returns the current status of the controller in JSON format.

**Response:**
```json
{
  "isLeader": true,
  "leaderSince": "2023-05-01T12:34:56Z",
  "lastLeaderTransition": "2023-05-01T12:34:56Z",
  "uptime": "1h2m3s",
  "leaderID": "controller-1",
  "clusterName": "primary",
  "clusterHealth": true,
  "redisConnected": true,
  "workloadStatus": {
    "processes": {
      "example-process": {
        "active": true,
        "namespace": "default"
      }
    },
    "cronjobs": {
      "example-cronjob": {
        "active": true,
        "namespace": "default"
      }
    }
  },
  "readyForTraffic": true,
  "version": "1.0.0",
  "buildInfo": "2023-05-01_12:34:56",
  "failoverCount": 0,
  "leadershipTransitions": 1,
  "currentLeader": "controller-1",
  "lastLeader": "",
  "lastLeadershipChangeReason": "normal transition"
}
```

### GET /debug/status

Returns detailed debug information about the controller in JSON format.

**Response:**
```json
{
  "healthStatus": {
    // Same as /status response
  },
  "uptime": "1h2m3s",
  "startTime": "2023-05-01T12:34:56Z",
  "goroutineCount": 10,
  "memoryStats": {
    "alloc": 1234567,
    "totalAlloc": 7654321,
    "sys": 12345678,
    "numGC": 42
  },
  "leadershipHistory": [
    {
      "from": "controller-1",
      "to": "controller-2",
      "timestamp": "2023-05-01T13:34:56Z",
      "reason": "controller-1 failed"
    }
  ]
}
```

## Workload Endpoints

### GET /api/workloads

Returns the status of all workloads managed by the controller.

**Response:**
```json
{
  "status": "success",
  "data": {
    "processes": {
      "example-process": {
        "name": "example-process",
        "type": "process",
        "active": true,
        "healthy": true,
        "lastTransition": "2023-05-01T12:34:56Z",
        "details": {
          "podPhase": "Running",
          "podIP": "10.0.0.1",
          "hostIP": "192.168.1.1"
        }
      }
    },
    "cronjobs": {
      "example-cronjob": {
        "name": "example-cronjob",
        "type": "cronjob",
        "active": true,
        "healthy": true,
        "lastTransition": "2023-05-01T12:34:56Z",
        "details": {
          "lastScheduleTime": "2023-05-01T12:30:00Z",
          "activeJobs": 0
        }
      }
    },
    "service": {
      "example-service": {
        "name": "example-service",
        "type": "service",
        "active": true,
        "healthy": true,
        "lastTransition": "2023-05-01T12:34:56Z",
        "details": {
          "availableReplicas": 1,
          "readyReplicas": 1,
          "updatedReplicas": 1
        }
      }
    },
    "persistent": {
      "example-persistent": {
        "name": "example-persistent",
        "type": "persistent",
        "active": true,
        "healthy": true,
        "lastTransition": "2023-05-01T12:34:56Z",
        "details": {
          "readyReplicas": 1,
          "currentReplicas": 1,
          "updatedReplicas": 1
        }
      }
    }
  }
}
```

### GET /api/workload-names

Returns a list of all workload names managed by the controller.

**Response:**
```json
{
  "status": "success",
  "data": [
    "example-process",
    "example-cronjob",
    "example-service",
    "example-persistent"
  ]
}
```

### GET /api/workloads/{name}

Returns the status of a specific workload.

**Parameters:**
- `name`: The name of the workload

**Response:**
```json
{
  "status": "success",
  "data": {
    "name": "example-process",
    "type": "process",
    "active": true,
    "healthy": true,
    "lastTransition": "2023-05-01T12:34:56Z",
    "details": {
      "podPhase": "Running",
      "podIP": "10.0.0.1",
      "hostIP": "192.168.1.1"
    }
  }
}
```

### GET /api/status

Returns the current status of the controller (same as `/status`).

**Response:**
```json
{
  "status": "success",
  "data": {
    // Same as /status response
  }
}
```

### GET /api/pods

Returns information about the Kubernetes pods managed by the controller.

**Response:**
```json
{
  "status": "success",
  "data": [
    {
      "name": "example-process-pod",
      "namespace": "default",
      "status": "Running",
      "node": "node-1",
      "age": "1h2m3s",
      "createdAt": "2023-05-01T12:34:56Z"
    },
    {
      "name": "example-service-6b4f9d8c7b-abcde",
      "namespace": "default",
      "status": "Running",
      "node": "node-2",
      "age": "1h2m3s",
      "createdAt": "2023-05-01T12:34:56Z"
    }
  ]
}
```

## Metrics Endpoint

### GET /metrics

Returns Prometheus metrics in the standard format.

**Response:**
```
# HELP k8_highlander_is_leader Indicates if this instance is currently the leader (1) or not (0)
# TYPE k8_highlander_is_leader gauge
k8_highlander_is_leader 1
# HELP k8_highlander_leadership_transitions_total Total number of leadership transitions
# TYPE k8_highlander_leadership_transitions_total counter
k8_highlander_leadership_transitions_total 1
# HELP k8_highlander_workload_status Status of managed workloads (1=active, 0=inactive)
# TYPE k8_highlander_workload_status gauge
k8_highlander_workload_status{name="example-process",namespace="default",type="process"} 1
k8_highlander_workload_status{name="example-cronjob",namespace="default",type="cronjob"} 1
# ... more metrics ...
```

## Dashboard

### GET /

Serves the dashboard UI.

## Error Responses

All API endpoints may return the following error responses:

### 400 Bad Request

Returned when the request is malformed or contains invalid parameters.

```json
{
  "error": "Invalid workload path"
}
```

### 404 Not Found

Returned when the requested resource does not exist.

```json
{
  "error": "Workload not found"
}
```

### 500 Internal Server Error

Returned when an unexpected error occurs.

```json
{
  "error": "Failed to list pods: <error message>"
}
```

## Authentication and Authorization

The K8 Highlander API does not currently implement authentication or authorization. It is recommended to secure the API using network policies, 
ingress controllers, or API gateways when deployed in production environments.

## Rate Limiting

The K8 Highlander API does not currently implement rate limiting. It is recommended to implement rate limiting at the 
ingress or API gateway level when deployed in production environments.

## API Versioning

The current API is considered v1 and is not explicitly versioned in the URL paths. Future versions of the API may include version information in the URL paths (e.g., `/api/v2/workloads`).

## Deprecation Policy

API endpoints may be deprecated in future versions of the K8 Highlander. Deprecated endpoints will continue to function for at least one major version after deprecation and will be documented as deprecated in the API reference.

