# K8 Highlander Cloud Function

A serverless implementation of K8 Highlander that runs as a Google Cloud Function with Cloud Spanner for state storage. This provides leader election and workload management without requiring Kubernetes infrastructure.

## 🌟 Features

- **Serverless Architecture**: Zero infrastructure management with Google Cloud Functions
- **Cloud Spanner Storage**: Globally consistent, highly available state storage
- **Embedded Dashboard**: Built-in web UI served directly from the Cloud Function
- **Automatic Scaling**: Scales from zero to handle traffic automatically
- **Cost Optimized**: Pay only for actual usage, no idle resource costs
- **Global Availability**: Deploy across multiple regions with Spanner's global consistency

## 🏗️ Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Cloud         │    │   Cloud         │    │   Cloud         │
│   Function      │◄──►│   Spanner       │◄──►│   Kubernetes    │
│   (Leader       │    │   (State        │    │   (Workloads)   │
│    Election)    │    │    Storage)     │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         │              ┌─────────────────┐              │
         └─────────────►│   Dashboard     │◄─────────────┘
                        │   (Web UI)      │
                        └─────────────────┘
```

**Components:**
- **Cloud Function**: Handles leader election, workload management, and serves dashboard
- **Cloud Spanner**: Stores leader state with global consistency and automatic failover
- **Embedded Dashboard**: Real-time monitoring interface served from the function
- **Service Account**: Automatic authentication to Spanner and Kubernetes APIs

## 🚀 Quick Start

### Prerequisites

- Google Cloud Project with billing enabled
- gcloud CLI installed and authenticated
- Cloud Functions and Spanner APIs enabled

### Free Testing (Recommended First)

```bash
# Clone and setup
git clone https://github.com/bhatti/k8-highlander.git
cd k8-highlander/functions/highlander

# Test with emulator (completely free)
make test-emulator
```

### Production Deployment

```bash
# Setup infrastructure (creates billable resources)
make setup

# Deploy to development
make deploy-dev

# Test the deployment
make test-real

# Get function URL
make get-url-dev
```

## 📋 Directory Structure

```
functions/highlander/
├── go.mod                    # Go module with replace directive
├── function.go              # Main Cloud Function entry point
├── static.go                # Embedded dashboard filesystem
├── Makefile                 # Complete automation
├── .env.dev.yaml           # Development environment
├── .env.prod.yaml          # Production environment
├── cloudbuild.yaml         # CI/CD pipeline
├── setup.sh                # Initial setup script
├── test-emulator.sh        # Free local testing
├── test-real-spanner.sh    # Real deployment testing
├── verify-setup.sh         # Setup verification
└── static/                 # Dashboard files (embedded)
    └── dashboard.html
```

## 🔧 Configuration

### Environment Variables

The Cloud Function is configured via environment variables in `.env.dev.yaml` and `.env.prod.yaml`:

```yaml
HIGHLANDER_STORAGE_TYPE: spanner
HIGHLANDER_SPANNER_PROJECT_ID: your-gcp-project
HIGHLANDER_SPANNER_INSTANCE_ID: highlander-instance
HIGHLANDER_SPANNER_DATABASE_ID: highlander-db
HIGHLANDER_ID: cf-dev
HIGHLANDER_TENANT: dev
HIGHLANDER_NAMESPACE: default
HIGHLANDER_CLUSTER_NAME: dev-cluster
```

### Service Account Permissions

The Cloud Function uses a service account with these IAM roles:

- `roles/spanner.databaseUser` - Read/write access to Spanner
- `roles/container.viewer` - View Kubernetes resources (optional)
- `roles/monitoring.viewer` - Access monitoring data (optional)

### Spanner Configuration

**Instance Settings:**
- **Processing Units**: 100 PU (cost-optimized, ~$65/month)
- **Region**: us-central1 (configurable)
- **Tables**: `leaders` and `workloads` (created automatically)

## 🧪 Testing

### Local Testing with Emulator

```bash
# Free testing with Spanner emulator
make test-emulator
```

**What it does:**
1. Starts Spanner emulator locally
2. Creates test instance and database
3. Runs Cloud Function locally
4. Tests all endpoints
5. Cleans up automatically

### Testing Real Deployment

```bash
# Test deployed function
make test-real

# Manual testing
FUNCTION_URL=$(make get-url-dev)
curl "$FUNCTION_URL/healthz"
curl "$FUNCTION_URL/api/dbinfo" | jq .
open "$FUNCTION_URL"  # Dashboard
```

## 📊 Monitoring

### Dashboard

Access the embedded dashboard at your function URL:

```bash
FUNCTION_URL=$(make get-url-dev)
echo "Dashboard: $FUNCTION_URL"
```

**Dashboard Features:**
- Real-time leader status
- Workload health monitoring
- Database connection status
- System metrics

### API Endpoints

| Endpoint | Description |
|----------|-------------|
| `/healthz` | Health check |
| `/readyz` | Readiness check |
| `/api/status` | Controller status |
| `/api/dbinfo` | Database connection info |
| `/api/workloads` | Workload status |
| `/debug/status` | Debug information |

### Logging

```bash
# View function logs
make logs-dev

# Real-time logs
gcloud functions logs tail highlander-api-dev --region=us-central1
```

## 💰 Cost Management

### Cost Breakdown

**Monthly Costs:**
- **Spanner**: ~$65/month (100 processing units)
- **Cloud Function**: ~$0.40/million requests + compute time
- **Total**: ~$70-80/month for development

### Cost Optimization

```bash
# Check current costs
make check-costs

# Use minimum processing units
gcloud spanner instances update highlander-instance \
    --processing-units=100

# Set up billing alerts
gcloud alpha billing budgets create \
    --billing-account=YOUR_BILLING_ACCOUNT \
    --display-name="Highlander Budget" \
    --budget-amount=100USD
```

### Free Alternatives

- **Development**: Use `make test-emulator` (completely free)
- **Testing**: Spanner emulator for all testing scenarios

## 🔄 Deployment Options

### Development Deployment

```bash
# Allow unauthenticated access for testing
make deploy-dev
```

**Settings:**
- Memory: 512MB
- Timeout: 540s
- Max Instances: 10
- Authentication: None (for testing)

### Production Deployment

```bash
# Requires authentication
make deploy-prod
```

**Settings:**
- Memory: 1GB
- Timeout: 540s
- Max Instances: 100
- Min Instances: 1
- Authentication: Required

### VPC Deployment (GKE Access)

```bash
# Deploy with VPC connector for GKE access
make deploy-with-vpc
```

**Prerequisites:**
```bash
# Create VPC connector
gcloud compute networks vpc-access connectors create highlander-vpc-connector \
    --region=us-central1 \
    --subnet=default \
    --min-instances=2 \
    --max-instances=3
```

## 🛠️ Development

### Local Development

```bash
# Install Functions Framework
go install github.com/GoogleCloudPlatform/functions-framework-go/funcframework@latest

# Set environment variables
export FUNCTION_TARGET=HighlanderAPI
export SPANNER_EMULATOR_HOST=localhost:9010
# ... other env vars

# Run locally
funcframework
```

### Building

```bash
# Build for Cloud Function
make build-cf

# Run tests
make test

# Run linter
make lint
```

### Module Resolution

The `go.mod` uses a replace directive to use the parent K8 Highlander module:

```go
module github.com/bhatti/k8-highlander/functions/highlander

replace github.com/bhatti/k8-highlander => ../..

require (
    github.com/bhatti/k8-highlander v0.0.0-00010101000000-000000000000
    // ... other dependencies
)
```

## 🚨 Troubleshooting

### Common Issues

#### 1. Permission Denied

```bash
# Check authentication
gcloud auth list

# Verify service account permissions
./verify-setup.sh

# Re-authenticate if needed
gcloud auth login
```

#### 2. Spanner Connection Failed

```bash
# Test Spanner access
gcloud spanner databases execute-sql highlander-db \
    --instance=highlander-instance \
    --sql="SELECT 1 as test"

# Check if instance exists
gcloud spanner instances describe highlander-instance
```

#### 3. Function Won't Start

```bash
# Check logs for errors
make logs-dev

# Verify environment variables
gcloud functions describe highlander-api-dev \
    --region=us-central1 \
    --format="table(serviceConfig.environmentVariables)"
```

#### 4. Module Resolution Issues

```bash
# Ensure replace directive is correct
cat go.mod | grep replace
# Should show: replace github.com/bhatti/k8-highlander => ../..

# Tidy modules
go mod tidy
```

### Debug Commands

```bash
# Complete setup verification
./verify-setup.sh

# Check all available commands
make help

# Test emulator first (free)
make test-emulator

# Manual function testing
curl "$FUNCTION_URL/api/status" | jq .
```

## 🔄 CI/CD Integration

### Cloud Build

The included `cloudbuild.yaml` provides automated deployment:

```bash
# Trigger build
gcloud builds submit --config=cloudbuild.yaml

# With environment override
gcloud builds submit --config=cloudbuild.yaml \
    --substitutions=_ENVIRONMENT=prod
```

### GitHub Actions

Example workflow:

```yaml
name: Deploy Cloud Function
on:
  push:
    branches: [main]
    paths: ['functions/highlander/**']

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v2
    - uses: google-github-actions/setup-gcloud@v0
      with:
        service_account_key: ${{ secrets.GCP_SA_KEY }}
        project_id: ${{ secrets.GCP_PROJECT_ID }}
    - name: Deploy function
      run: |
        cd functions/highlander
        make deploy-prod
```

## 🧹 Cleanup

### Selective Cleanup

```bash
# Delete functions only
make delete-dev
make delete

# Delete Spanner only (saves most money)
make cleanup-spanner
```

### Complete Cleanup

```bash
# Delete everything (with confirmation)
make cleanup-all
```

**⚠️ Warning**: This permanently deletes all data and resources.

## 📚 Available Commands

```bash
# Setup and Testing
make test-emulator        # Free local testing
make setup               # Create real Spanner (costs money)
make deploy-dev          # Deploy development function
make test-real           # Test real deployment
./verify-setup.sh        # Check setup status

# Management
make logs-dev            # View logs
make get-url-dev         # Get function URL
make check-costs         # Check Spanner costs

# Cleanup
make cleanup-all         # Delete everything
```

Run `make help` for a complete list of available commands.

## 🔗 Integration with K8 Highlander

This Cloud Function implementation:

- **Shares Core Logic**: Uses the same workload management code as the Kubernetes version
- **API Compatible**: Provides the same REST API endpoints
- **Dashboard Compatible**: Uses the same dashboard interface
- **Storage Compatible**: Can share Spanner storage with Kubernetes deployments

### Hybrid Deployments

You can run both Kubernetes and Cloud Function versions simultaneously:

- **Cloud Function**: Handles leader election and coordination
- **Kubernetes**: Runs the actual workloads
- **Shared State**: Both use the same Spanner database for coordination

## 🎯 When to Use Cloud Function vs Kubernetes

### Use Cloud Function When:
- You want zero infrastructure management
- You need global leader election across regions
- You prefer serverless cost model (pay per use)
- You want automatic scaling and high availability
- You're running lightweight coordination tasks

### Use Kubernetes When:
- You need to run workloads directly in the cluster
- You have existing Kubernetes infrastructure
- You need tight integration with Kubernetes resources
- You want full control over the runtime environment

## 📈 Performance Characteristics

- **Cold Start**: ~2-3 seconds for first request
- **Warm Requests**: <100ms response time
- **Concurrency**: Up to 1000 concurrent requests per instance
- **Availability**: 99.95% SLA (Google Cloud Functions)
- **Global Latency**: <200ms with Spanner multi-region

## 🔐 Security

### Authentication
- **Development**: Unauthenticated (for testing)
- **Production**: Google Cloud IAM authentication required
- **API Access**: Service account based authentication

### Network Security
- **HTTPS Only**: All traffic encrypted in transit
- **VPC Support**: Optional VPC connector for private network access
- **IAM Integration**: Fine-grained access control via Google Cloud IAM

### Data Security
- **Encryption at Rest**: Spanner automatically encrypts all data
- **Encryption in Transit**: All connections use TLS
- **Access Logging**: Full audit trail in Cloud Logging
-
