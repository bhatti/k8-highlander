# Architecture Overview

The K8 Highlander is designed to provide reliable singleton workload management in Kubernetes clusters. 
This document explains the architecture, components, and interactions of the system.

## System Components

The K8 Highlander consists of several key components:

1. **Leader Election**: Ensures only one controller instance is active at a time
2. **Workload Manager**: Manages different types of workloads
3. **Monitoring Server**: Provides metrics and status information
4. **HTTP Server**: Serves the dashboard and API endpoints

## High-Level Architecture

The following diagram shows the high-level architecture of the K8 Highlander:

![architecture](images/architecture.png)
```mermaid
graph TD
    subgraph "Kubernetes Cluster"
        subgraph "Controller Instances"
            C1[Controller Instance 1<br>Leader]
            C2[Controller Instance 2<br>Follower]
            C3[Controller Instance 3<br>Follower]
        end
        
        subgraph "Managed Workloads"
            W1[Process Workloads]
            W2[CronJob Workloads]
            W3[Service Workloads]
            W4[Persistent Workloads]
        end
        
        subgraph "Kubernetes API"
            K[Kubernetes API Server]
        end
    end
    
    subgraph "External Storage"
        DB[(Leader State Storage<br>Redis/PostgreSQL)]
    end
    
    C1 -->|Manages| W1
    C1 -->|Manages| W2
    C1 -->|Manages| W3
    C1 -->|Manages| W4
    
    C1 -->|Creates/Updates/Deletes| K
    C2 -->|Monitors| K
    C3 -->|Monitors| K
    
    C1 -->|Acquires Lock| DB
    C2 -->|Checks Lock| DB
    C3 -->|Checks Lock| DB
    
    C1 -.->|Failover| C2
    C2 -.->|Failover| C3
    
    classDef leader fill:#d4edda,stroke:#28a745,stroke-width:2px;
    classDef follower fill:#f8d7da,stroke:#dc3545,stroke-width:1px;
    classDef workload fill:#e2f0fb,stroke:#0275d8,stroke-width:1px;
    classDef k8s fill:#f5f5f5,stroke:#6c757d,stroke-width:1px;
    classDef storage fill:#fff3cd,stroke:#ffc107,stroke-width:1px;
    
    class C1 leader;
    class C2,C3 follower;
    class W1,W2,W3,W4 workload;
    class K k8s;
    class DB storage;
```

## Leader Election Process

The leader election process ensures that only one controller instance is active at a time. 
This is critical for preventing duplicate workloads.

![Leader Election](images/leader_election.png)
```mermaid
sequenceDiagram
    participant C1 as Controller 1
    participant C2 as Controller 2
    participant DB as Redis/DB
    participant K8s as Kubernetes API
    
    Note over C1,C2: Startup Phase
    
    C1->>DB: Try to acquire lock
    DB-->>C1: Lock acquired (becomes leader)
    C2->>DB: Try to acquire lock
    DB-->>C2: Lock already held (becomes follower)
    
    Note over C1,C2: Normal Operation
    
    loop Every 5 seconds
        C1->>DB: Renew lock
        DB-->>C1: Lock renewed
        C2->>DB: Check lock status
        DB-->>C2: Lock held by C1
    end
    
    Note over C1,C2: Failover Scenario
    
    C1-xDB: Connection lost or process crashes
    
    loop Lock TTL period (15s)
        DB->>DB: Lock expires
    end
    
    C2->>DB: Check lock status
    DB-->>C2: Lock expired
    C2->>DB: Try to acquire lock
    DB-->>C2: Lock acquired (becomes new leader)
    
    C2->>K8s: Start managing workloads
    K8s-->>C2: Workloads running
```

## Workload Management

The Workload Manager is responsible for creating, updating, and deleting workloads in the Kubernetes cluster. 
It supports different types of workloads:
![Workload Management](images/workloads.png)

```mermaid
graph TD
    subgraph "Controller"
        LE[Leader Election]
        WM[Workload Manager]
        MS[Monitoring Server]
        
        LE -->|Leader Status| WM
        WM -->|Workload Status| MS
    end
    
    subgraph "Workload Types"
        PM[Process Manager]
        CM[CronJob Manager]
        SM[Service Manager]
        PSM[Persistent Manager]
        
        WM -->|Manages| PM
        WM -->|Manages| CM
        WM -->|Manages| SM
        WM -->|Manages| PSM
    end
    
    subgraph "Kubernetes Resources"
        P[Pods]
        CJ[CronJobs]
        D[Deployments]
        SS[StatefulSets]
        SVC[Services]
        
        PM -->|Creates/Manages| P
        CM -->|Creates/Manages| CJ
        SM -->|Creates/Manages| D
        SM -->|Creates/Manages| SVC
        PSM -->|Creates/Manages| SS
        PSM -->|Creates/Manages| SVC
    end
    
    classDef controller fill:#e2f0fb,stroke:#0275d8,stroke-width:1px;
    classDef manager fill:#d4edda,stroke:#28a745,stroke-width:1px;
    classDef resource fill:#f5f5f5,stroke:#6c757d,stroke-width:1px;
    
    class LE,WM,MS controller;
    class PM,CM,SM,PSM manager;
    class P,CJ,D,SS,SVC resource;
```

### Workload Types

1. **Process Workloads**: Single-instance processes running in pods
2. **CronJob Workloads**: Scheduled tasks that run at specified intervals
3. **Service Workloads**: Continuously running services with a specified number of replicas
4. **Persistent Workloads**: Stateful applications with persistent storage

## Multi-Tenant Architecture

The K8 Highlander supports multi-tenant deployments, where different tenants can have their own leader 
election and workload management:

![Multi-Tenant](images/tenants.png)

```mermaid
graph TD
    subgraph "Tenant A"
        C1A[Controller A1<br>Leader]
        C2A[Controller A2<br>Follower]
        WA[Tenant A Workloads]
        
        C1A -->|Manages| WA
    end
    
    subgraph "Tenant B"
        C1B[Controller B1<br>Leader]
        C2B[Controller B2<br>Follower]
        WB[Tenant B Workloads]
        
        C1B -->|Manages| WB
    end
    
    subgraph "Shared Storage"
        DB[(Redis/PostgreSQL)]
    end
    
    C1A -->|Lock: tenant-a| DB
    C2A -->|Check: tenant-a| DB
    
    C1B -->|Lock: tenant-b| DB
    C2B -->|Check: tenant-b| DB
    
    classDef leader fill:#d4edda,stroke:#28a745,stroke-width:2px;
    classDef follower fill:#f8d7da,stroke:#dc3545,stroke-width:1px;
    classDef workload fill:#e2f0fb,stroke:#0275d8,stroke-width:1px;
    classDef storage fill:#fff3cd,stroke:#ffc107,stroke-width:1px;
    
    class C1A,C1B leader;
    class C2A,C2B follower;
    class WA,WB workload;
    class DB storage;
```

## Component Interactions

The following diagram shows the interactions between the different components of the K8 Highlander:
![Components](images/components.png)

```mermaid
graph TD
    subgraph "Controller Components"
        CMD[Command Line Interface]
        CTRL[Controller]
        LE[Leader Election]
        WM[Workload Manager]
        MS[Monitoring Server]
        HTTP[HTTP Server]
        
        CMD -->|Initializes| CTRL
        CTRL -->|Creates| LE
        CTRL -->|Creates| WM
        CTRL -->|Creates| MS
        CTRL -->|Creates| HTTP
        
        LE -->|Leader Status| WM
        LE -->|Leader Status| MS
        WM -->|Workload Status| MS
        MS -->|Metrics & Status| HTTP
    end
    
    subgraph "External Systems"
        K8S[Kubernetes API]
        REDIS[(Redis)]
        DB[(PostgreSQL)]
        PROM[Prometheus]
        
        LE -->|Lock Management| REDIS
        LE -.->|Alternative Lock| DB
        WM -->|Resource Management| K8S
        HTTP -->|Metrics Scraping| PROM
    end
    
    classDef component fill:#e2f0fb,stroke:#0275d8,stroke-width:1px;
    classDef external fill:#f5f5f5,stroke:#6c757d,stroke-width:1px;
    
    class CMD,CTRL,LE,WM,MS,HTTP component;
    class K8S,REDIS,DB,PROM external;
```

## Data Flow

The following diagram shows the data flow through the K8 Highlander:
![DFD](images/dfd.png)

```mermaid
flowchart TD
    subgraph "Input"
        CFG[Configuration File]
        ENV[Environment Variables]
        CLI[Command Line Flags]
    end
    
    subgraph "Controller"
        INIT[Initialization]
        LE[Leader Election]
        WM[Workload Management]
        MON[Monitoring]
        
        INIT -->|Configuration| LE
        INIT -->|Configuration| WM
        INIT -->|Configuration| MON
        
        LE -->|Leader Status| WM
        LE -->|Leader Status| MON
        WM -->|Workload Status| MON
    end
    
    subgraph "Storage"
        REDIS[(Redis)]
        DB[(PostgreSQL)]
    end
    
    subgraph "Kubernetes"
        API[API Server]
        PODS[Pods]
        JOBS[Jobs]
        DEPLOY[Deployments]
        STS[StatefulSets]
    end
    
    subgraph "Output"
        METRICS[Prometheus Metrics]
        DASH[Dashboard]
        LOGS[Logs]
    end
    
    CFG -->|Read| INIT
    ENV -->|Override| INIT
    CLI -->|Override| INIT
    
    LE <-->|Lock Management| REDIS
    LE <-.->|Alternative Lock| DB
    
    WM -->|Create/Update/Delete| API
    API -->|Manage| PODS
    API -->|Manage| JOBS
    API -->|Manage| DEPLOY
    API -->|Manage| STS
    
    MON -->|Expose| METRICS
    MON -->|Visualize| DASH
    
    INIT -->|Write| LOGS
    LE -->|Write| LOGS
    WM -->|Write| LOGS
    MON -->|Write| LOGS
    
    classDef input fill:#e2f0fb,stroke:#0275d8,stroke-width:1px;
    classDef controller fill:#d4edda,stroke:#28a745,stroke-width:1px;
    classDef storage fill:#fff3cd,stroke:#ffc107,stroke-width:1px;
    classDef k8s fill:#f8d7da,stroke:#dc3545,stroke-width:1px;
    classDef output fill:#f5f5f5,stroke:#6c757d,stroke-width:1px;
    
    class CFG,ENV,CLI input;
    class INIT,LE,WM,MON controller;
    class REDIS,DB storage;
    class API,PODS,JOBS,DEPLOY,STS k8s;
    class METRICS,DASH,LOGS output;
```

## Storage Options

The K8 Highlander supports two storage options for leader election:

1. **Redis**: Fast, in-memory storage with optional persistence
2. **PostgreSQL**: Relational database for more robust persistence

Both options provide the necessary functionality for leader election, but Redis is generally faster 
while PostgreSQL provides stronger durability guarantees.

## Failover Process

When a leader controller fails, the following process occurs:

1. The leader's lock in Redis/PostgreSQL expires after the TTL period (default: 60s)
2. Follower controllers detect the expired lock
3. One follower acquires the lock and becomes the new leader
4. The new leader starts managing workloads
5. Workloads are recreated if necessary

This process ensures high availability of the controller and the workloads it manages.

## Security Considerations

The K8 Highlander requires certain permissions to manage workloads in Kubernetes:

- Create, update, and delete pods, deployments, statefulsets, and services
- Read and write access to the configured namespace
- Access to the Redis or PostgreSQL storage

It's recommended to run the controller with the principle of least privilege, granting only the permissions 
it needs to function.

## Performance Considerations

The K8 Highlander is designed to be lightweight and efficient. However, there are some performance considerations:

- Redis is recommended for high-performance environments
- Lock TTL should be tuned based on workload startup/shutdown time, network latency and expected load
- Resource requests and limits should be set appropriately for the controller pods
- Monitoring should be enabled to track performance metrics

## Next Steps

- See the [Configuration Reference](configuration.md) for details on configuring the controller
- See the [API Reference](api-reference.md) for details on the API endpoints
- See the [Metrics Reference](metrics.md) for details on the available metrics
- See the [Deployment Strategies](deployment.md) for details on deploying the controller

