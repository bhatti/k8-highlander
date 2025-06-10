# Cloud Function parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOLINT=golangci-lint

# GCP and Cloud Function Variables
PROJECT_ID ?= $(shell gcloud config get-value project)
FUNCTION_NAME ?= highlander-api
REGION ?= us-central1
RUNTIME ?= go121

# Environment configurations
ENVIRONMENT ?= dev

# Spanner configuration
SPANNER_INSTANCE_ID ?= highlander-instance
SPANNER_DATABASE_ID ?= highlander-db
SERVICE_ACCOUNT_NAME ?= highlander-cf
SERVICE_ACCOUNT_EMAIL ?= $(SERVICE_ACCOUNT_NAME)@$(PROJECT_ID).iam.gserviceaccount.com

# Emulator configuration
EMULATOR_PROJECT_ID ?= test-project
EMULATOR_INSTANCE_ID ?= test-instance
EMULATOR_DATABASE_ID ?= test-database

# Source files
SRC=$(shell find . -name "*.go" -type f)
PKG_LIST=$(shell go list ./... | grep -v /vendor/)

# Default target
.PHONY: all
all: test lint build-cf

# Build for Cloud Function
.PHONY: build-cf
build-cf:
	@echo "Building for Cloud Function..."
	$(GOMOD) tidy
	$(GOBUILD) -o bin/function .

# Clean build artifacts
.PHONY: clean
clean:
	$(GOCLEAN)
	rm -rf bin/

# Run unit tests
.PHONY: test
test:
	$(GOTEST) -v -race -cover $(PKG_LIST)

# Run linter
.PHONY: lint
lint:
	$(GOLINT) run ./...

# Format code
.PHONY: fmt
fmt:
	gofmt -s -w $(SRC)

# Tidy go modules
.PHONY: tidy
tidy:
	$(GOMOD) tidy

# Verify dependencies
.PHONY: verify
verify: tidy
	$(GOMOD) verify

# Copy static files (dashboard)
.PHONY: prepare-static
prepare-static:
	@echo "Preparing static files..."
	mkdir -p static
	cp -r ../../static/* static/ 2>/dev/null || cp -r ../../dashboard/* static/ 2>/dev/null || echo "No static files found to copy"

# Enable required GCP APIs
.PHONY: enable-apis
enable-apis:
	@echo "Enabling required GCP APIs..."
	gcloud services enable cloudfunctions.googleapis.com
	gcloud services enable spanner.googleapis.com
	gcloud services enable container.googleapis.com
	gcloud services enable monitoring.googleapis.com
	gcloud services enable cloudbuild.googleapis.com

# Create service account with Spanner permissions
.PHONY: setup-service-account
setup-service-account:
	@echo "Setting up service account: $(SERVICE_ACCOUNT_EMAIL)"
	@if ! gcloud iam service-accounts describe $(SERVICE_ACCOUNT_EMAIL) --project=$(PROJECT_ID) >/dev/null 2>&1; then \
		echo "Creating service account: $(SERVICE_ACCOUNT_EMAIL)"; \
		gcloud iam service-accounts create $(SERVICE_ACCOUNT_NAME) \
			--display-name="Highlander Cloud Function Service Account" \
			--description="Service account for Highlander Cloud Function with Spanner access" \
			--project=$(PROJECT_ID); \
	else \
		echo "Service account $(SERVICE_ACCOUNT_EMAIL) already exists"; \
	fi
	@echo "Adding IAM roles to service account..."
	gcloud projects add-iam-policy-binding $(PROJECT_ID) \
		--member="serviceAccount:$(SERVICE_ACCOUNT_EMAIL)" \
		--role="roles/spanner.databaseUser"
	gcloud projects add-iam-policy-binding $(PROJECT_ID) \
		--member="serviceAccount:$(SERVICE_ACCOUNT_EMAIL)" \
		--role="roles/container.viewer"
	gcloud projects add-iam-policy-binding $(PROJECT_ID) \
		--member="serviceAccount:$(SERVICE_ACCOUNT_EMAIL)" \
		--role="roles/monitoring.viewer"

# Setup real Spanner instance and database
.PHONY: setup-spanner
setup-spanner:
	@echo "Setting up Spanner instance and database..."
	@if ! gcloud spanner instances describe $(SPANNER_INSTANCE_ID) --project=$(PROJECT_ID) >/dev/null 2>&1; then \
		echo "Creating Spanner instance: $(SPANNER_INSTANCE_ID)"; \
		gcloud spanner instances create $(SPANNER_INSTANCE_ID) \
			--config=regional-$(REGION) \
			--description="Highlander Instance" \
			--processing-units=100 \
			--project=$(PROJECT_ID); \
	else \
		echo "Spanner instance $(SPANNER_INSTANCE_ID) already exists"; \
	fi
	@if ! gcloud spanner databases describe $(SPANNER_DATABASE_ID) --instance=$(SPANNER_INSTANCE_ID) --project=$(PROJECT_ID) >/dev/null 2>&1; then \
		echo "Creating Spanner database: $(SPANNER_DATABASE_ID)"; \
		gcloud spanner databases create $(SPANNER_DATABASE_ID) \
			--instance=$(SPANNER_INSTANCE_ID) \
			--project=$(PROJECT_ID); \
		echo "Creating database schema..."; \
		gcloud spanner databases ddl update $(SPANNER_DATABASE_ID) \
			--instance=$(SPANNER_INSTANCE_ID) \
			--ddl-file=schema.sql \
			--project=$(PROJECT_ID); \
	else \
		echo "Spanner database $(SPANNER_DATABASE_ID) already exists"; \
	fi

# Create database schema file
.PHONY: create-schema
create-schema:
	@echo "Creating schema.sql file..."
	@cat > schema.sql << 'EOF'
CREATE TABLE leaders (
    id STRING(MAX) NOT NULL,
    tenant STRING(MAX) NOT NULL,
    namespace STRING(MAX) NOT NULL,
    cluster_name STRING(MAX) NOT NULL,
    leader_id STRING(MAX) NOT NULL,
    acquired_at TIMESTAMP NOT NULL,
    renewed_at TIMESTAMP NOT NULL,
    lease_duration INT64 NOT NULL,
    workload_version STRING(MAX),
    metadata JSON,
) PRIMARY KEY (id, tenant, namespace, cluster_name);

CREATE TABLE workloads (
    id STRING(MAX) NOT NULL,
    tenant STRING(MAX) NOT NULL,
    namespace STRING(MAX) NOT NULL,
    cluster_name STRING(MAX) NOT NULL,
    name STRING(MAX) NOT NULL,
    spec JSON NOT NULL,
    status JSON,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    version INT64 NOT NULL,
) PRIMARY KEY (id, tenant, namespace, cluster_name, name);
EOF

# Create environment files
.PHONY: create-env-files
create-env-files:
	@echo "Creating environment files..."
	@cat > .env.dev.yaml << 'EOF'
HIGHLANDER_STORAGE_TYPE: spanner
HIGHLANDER_SPANNER_PROJECT_ID: $(PROJECT_ID)
HIGHLANDER_SPANNER_INSTANCE_ID: $(SPANNER_INSTANCE_ID)
HIGHLANDER_SPANNER_DATABASE_ID: $(SPANNER_DATABASE_ID)
HIGHLANDER_ID: cf-dev
HIGHLANDER_TENANT: dev
HIGHLANDER_NAMESPACE: default
HIGHLANDER_CLUSTER_NAME: dev-cluster
EOF
	@cat > .env.prod.yaml << 'EOF'
HIGHLANDER_STORAGE_TYPE: spanner
HIGHLANDER_SPANNER_PROJECT_ID: $(PROJECT_ID)
HIGHLANDER_SPANNER_INSTANCE_ID: $(SPANNER_INSTANCE_ID)
HIGHLANDER_SPANNER_DATABASE_ID: $(SPANNER_DATABASE_ID)
HIGHLANDER_ID: cf-prod
HIGHLANDER_TENANT: prod
HIGHLANDER_NAMESPACE: default
HIGHLANDER_CLUSTER_NAME: prod-cluster
EOF
	@cat > .env.local.yaml << 'EOF'
HIGHLANDER_STORAGE_TYPE: spanner
HIGHLANDER_SPANNER_PROJECT_ID: $(EMULATOR_PROJECT_ID)
HIGHLANDER_SPANNER_INSTANCE_ID: $(EMULATOR_INSTANCE_ID)
HIGHLANDER_SPANNER_DATABASE_ID: $(EMULATOR_DATABASE_ID)
HIGHLANDER_ID: cf-local
HIGHLANDER_TENANT: test
HIGHLANDER_NAMESPACE: default
HIGHLANDER_CLUSTER_NAME: test-cluster
EOF

# Full setup for Cloud Function with real Spanner
.PHONY: setup
setup: enable-apis setup-service-account create-schema setup-spanner create-env-files prepare-static
	@echo "Cloud Function setup with real Spanner complete!"
	@echo "Next steps:"
	@echo "  make deploy-dev    # Deploy to development"
	@echo "  make test-real     # Test real Spanner deployment"

# Setup Spanner emulator
.PHONY: setup-emulator
setup-emulator:
	@echo "Setting up Spanner emulator..."
	@pkill -f "cloud-spanner-emulator" || true
	@sleep 2
	@echo "Starting Spanner emulator..."
	@gcloud emulators spanner start --host-port=localhost:9010 &
	@sleep 10
	@echo "Creating emulator configuration..."
	@gcloud config configurations activate emulator 2>/dev/null || gcloud config configurations create emulator
	@gcloud config set api_endpoint_overrides/spanner http://localhost:9020/ --configuration=emulator
	@gcloud config set auth/disable_credentials true --configuration=emulator
	@gcloud config set project $(EMULATOR_PROJECT_ID) --configuration=emulator
	@gcloud config set account fake@example.com --configuration=emulator
	@echo "Creating emulator instance and database..."
	@gcloud spanner instances create $(EMULATOR_INSTANCE_ID) \
		--config=emulator-config \
		--description="Test Instance" \
		--nodes=1 \
		--configuration=emulator
	@gcloud spanner databases create $(EMULATOR_DATABASE_ID) \
		--instance=$(EMULATOR_INSTANCE_ID) \
		--configuration=emulator
	@echo "Creating schema in emulator..."
	@gcloud spanner databases ddl update $(EMULATOR_DATABASE_ID) \
		--instance=$(EMULATOR_INSTANCE_ID) \
		--ddl-file=schema.sql \
		--configuration=emulator
	@gcloud config configurations activate default
	@echo "Spanner emulator setup complete!"

# Test with emulator
# Test with emulator
.PHONY: test-emulator
test-emulator: create-schema setup-emulator
	@echo "Testing with Spanner emulator..."
	@export SPANNER_EMULATOR_HOST=localhost:9010 && \
	export FUNCTION_TARGET=HighlanderAPI && \
	export HIGHLANDER_STORAGE_TYPE=spanner && \
	export HIGHLANDER_SPANNER_PROJECT_ID=$(EMULATOR_PROJECT_ID) && \
	export HIGHLANDER_SPANNER_INSTANCE_ID=$(EMULATOR_INSTANCE_ID) && \
	export HIGHLANDER_SPANNER_DATABASE_ID=$(EMULATOR_DATABASE_ID) && \
	export HIGHLANDER_ID=cf-local && \
	export HIGHLANDER_TENANT=test && \
	export HIGHLANDER_NAMESPACE=default && \
	export HIGHLANDER_CLUSTER_NAME=test-cluster && \
	export PORT=8080 && \
	echo "Starting local function..." && \
	(go run github.com/GoogleCloudPlatform/functions-framework-go/funcframework@latest || \
	 go run github.com/GoogleCloudPlatform/functions-framework-go/funcframework@v1.8.1 || \
	 (echo 'package main\nimport (\n    "log"\n    "os"\n    "github.com/GoogleCloudPlatform/functions-framework-go/funcframework"\n)\nfunc main() {\n    if err := funcframework.Start(os.Getenv("PORT")); err != nil {\n        log.Fatalf("funcframework.Start: %v\\n", err)\n    }\n}' > temp_main.go && go run temp_main.go)) &
	@sleep 8
	@echo "Testing local function endpoints..."
	@curl -f "http://localhost:8080/healthz" || (echo "Health check failed" && pkill -f funcframework && pkill -f "cloud-spanner-emulator" && exit 1)
	@echo "Health check passed!"
	@curl -s "http://localhost:8080/api/dbinfo" | jq . || echo "DB info test completed"
	@curl -s "http://localhost:8080/api/status" | jq . || echo "Status test completed"
	@pkill -f funcframework || true
	@pkill -f "cloud-spanner-emulator" || true
	@rm -f temp_main.go || true
	@echo "✅ Emulator tests completed!"

# Deploy to development
.PHONY: deploy-dev
deploy-dev: prepare-static build-cf
	@echo "Deploying to development with real Spanner..."
	gcloud functions deploy $(FUNCTION_NAME)-dev \
		--gen2 \
		--runtime=$(RUNTIME) \
		--region=$(REGION) \
		--source=. \
		--entry-point=HighlanderAPI \
		--trigger=http \
		--allow-unauthenticated \
		--env-vars-file=.env.dev.yaml \
		--service-account=$(SERVICE_ACCOUNT_EMAIL) \
		--memory=512MB \
		--timeout=540s \
		--max-instances=10 \
		--project=$(PROJECT_ID)

# Deploy to production
.PHONY: deploy-prod
deploy-prod: prepare-static build-cf
	@echo "Deploying to production with real Spanner..."
	gcloud functions deploy $(FUNCTION_NAME) \
		--gen2 \
		--runtime=$(RUNTIME) \
		--region=$(REGION) \
		--source=. \
		--entry-point=HighlanderAPI \
		--trigger=http \
		--no-allow-unauthenticated \
		--env-vars-file=.env.prod.yaml \
		--service-account=$(SERVICE_ACCOUNT_EMAIL) \
		--memory=1GB \
		--timeout=540s \
		--max-instances=100 \
		--min-instances=1 \
		--project=$(PROJECT_ID)

# Deploy with VPC connector (for GKE access)
.PHONY: deploy-with-vpc
deploy-with-vpc: prepare-static build-cf
	@echo "Deploying with VPC connector..."
	gcloud functions deploy $(FUNCTION_NAME) \
		--gen2 \
		--runtime=$(RUNTIME) \
		--region=$(REGION) \
		--source=. \
		--entry-point=HighlanderAPI \
		--trigger=http \
		--vpc-connector=projects/$(PROJECT_ID)/locations/$(REGION)/connectors/highlander-vpc-connector \
		--env-vars-file=.env.prod.yaml \
		--service-account=$(SERVICE_ACCOUNT_EMAIL) \
		--memory=1GB \
		--timeout=540s \
		--project=$(PROJECT_ID)

# Test real Spanner deployment
.PHONY: test-real
test-real:
	@echo "Testing real Spanner deployment..."
	@FUNCTION_URL=$$(gcloud functions describe $(FUNCTION_NAME) --region=$(REGION) --project=$(PROJECT_ID) --format="value(serviceConfig.uri)" 2>/dev/null) || \
	FUNCTION_URL=$$(gcloud functions describe $(FUNCTION_NAME)-dev --region=$(REGION) --project=$(PROJECT_ID) --format="value(serviceConfig.uri)") && \
	echo "Function URL: $$FUNCTION_URL" && \
	echo "Testing health endpoint..." && \
	curl -f -s "$$FUNCTION_URL/healthz" && echo " ✅ Health check passed" && \
	echo "Testing database connection..." && \
	DB_INFO=$$(curl -f -s "$$FUNCTION_URL/api/dbinfo") && \
	echo "$$DB_INFO" | jq . && \
	if echo "$$DB_INFO" | jq -r '.data.connected' | grep -q "true"; then \
		echo "✅ Database connection successful"; \
	else \
		echo "❌ Database connection failed"; \
		exit 1; \
	fi && \
	echo "Testing status endpoint..." && \
	curl -f -s "$$FUNCTION_URL/api/status" | jq . && \
	echo "Testing workloads endpoint..." && \
	curl -f -s "$$FUNCTION_URL/api/workloads" | jq . && \
	echo "🎉 All real Spanner tests passed!"

# Test development deployment
.PHONY: test-dev
test-dev:
	@echo "Testing development deployment..."
	@FUNCTION_URL=$$(gcloud functions describe $(FUNCTION_NAME)-dev --region=$(REGION) --project=$(PROJECT_ID) --format="value(serviceConfig.uri)") && \
	echo "Function URL: $$FUNCTION_URL" && \
	curl -f -s "$$FUNCTION_URL/healthz" && echo " ✅ Health check passed" && \
	curl -f -s "$$FUNCTION_URL/api/dbinfo" | jq . && \
	echo "🎉 Development tests passed!"

# View function logs
.PHONY: logs
logs:
	@echo "Viewing function logs..."
	gcloud functions logs read $(FUNCTION_NAME) --region=$(REGION) --limit=50 --project=$(PROJECT_ID)

# View development function logs
.PHONY: logs-dev
logs-dev:
	@echo "Viewing development function logs..."
	gcloud functions logs read $(FUNCTION_NAME)-dev --region=$(REGION) --limit=50 --project=$(PROJECT_ID)

# Get function URLs
.PHONY: get-url
get-url:
	@echo "Production function URL:"
	@gcloud functions describe $(FUNCTION_NAME) --region=$(REGION) --project=$(PROJECT_ID) --format="value(serviceConfig.uri)" 2>/dev/null || echo "Production function not found"

.PHONY: get-url-dev
get-url-dev:
	@echo "Development function URL:"
	@gcloud functions describe $(FUNCTION_NAME)-dev --region=$(REGION) --project=$(PROJECT_ID) --format="value(serviceConfig.uri)" 2>/dev/null || echo "Development function not found"

# Delete functions
.PHONY: delete
delete:
	@echo "Deleting production function..."
	gcloud functions delete $(FUNCTION_NAME) --region=$(REGION) --project=$(PROJECT_ID) --quiet

.PHONY: delete-dev
delete-dev:
	@echo "Deleting development function..."
	gcloud functions delete $(FUNCTION_NAME)-dev --region=$(REGION) --project=$(PROJECT_ID) --quiet

# Cleanup Spanner resources
.PHONY: cleanup-spanner
cleanup-spanner:
	@echo "⚠️  WARNING: This will delete your Spanner database and instance!"
	@echo "⚠️  All data will be permanently lost!"
	@read -p "Are you sure you want to continue? Type 'yes' to confirm: " confirm && \
	if [ "$$confirm" = "yes" ]; then \
		echo "Deleting Spanner database..."; \
		gcloud spanner databases delete $(SPANNER_DATABASE_ID) --instance=$(SPANNER_INSTANCE_ID) --project=$(PROJECT_ID) --quiet || echo "Database deletion failed or already deleted"; \
		echo "Deleting Spanner instance..."; \
		gcloud spanner instances delete $(SPANNER_INSTANCE_ID) --project=$(PROJECT_ID) --quiet || echo "Instance deletion failed or already deleted"; \
		echo "✅ Spanner resources deleted"; \
	else \
		echo "Cleanup cancelled"; \
	fi

# Stop emulator
.PHONY: stop-emulator
stop-emulator:
	@echo "Stopping Spanner emulator..."
	@pkill -f "cloud-spanner-emulator" || echo "Emulator not running"
	@pkill -f funcframework || echo "Functions framework not running"

# Full cleanup
.PHONY: cleanup-all
cleanup-all: delete delete-dev stop-emulator
	@echo "⚠️  WARNING: This will delete ALL resources including Spanner!"
	@read -p "Are you sure? Type 'yes' to confirm: " confirm && \
	if [ "$$confirm" = "yes" ]; then \
		$(MAKE) cleanup-spanner; \
		echo "Deleting service account..."; \
		gcloud iam service-accounts delete $(SERVICE_ACCOUNT_EMAIL) --project=$(PROJECT_ID) --quiet || echo "Service account deletion failed"; \
		echo "✅ All resources cleaned up"; \
	else \
		echo "Cleanup cancelled"; \
	fi

# Check Spanner costs
.PHONY: check-costs
check-costs:
	@echo "Checking Spanner instance configuration..."
	@gcloud spanner instances describe $(SPANNER_INSTANCE_ID) --project=$(PROJECT_ID) --format="table(name,config,nodeCount,processingUnits,displayName)" || echo "Instance not found"

# Create setup script
.PHONY: create-setup-script
create-setup-script:
	@echo "Creating setup.sh script..."
	@cat > setup.sh << 'EOF'
#!/bin/bash
# Highlander Cloud Function Setup Script
set -e

echo "🚀 Setting up Highlander Cloud Function..."

# Check if we're in the right directory
if [[ ! -f "function.go" ]]; then
    echo "❌ Error: Please run this script from the functions/highlander directory"
    exit 1
fi

# Check if gcloud is installed and authenticated
if ! command -v gcloud &> /dev/null; then
    echo "❌ Error: gcloud CLI is not installed. Please install it first."
    exit 1
fi

# Get project ID
PROJECT_ID=$(gcloud config get-value project 2>/dev/null)
if [[ -z "$PROJECT_ID" ]]; then
    echo "❌ Error: No GCP project set. Please run: gcloud config set project YOUR_PROJECT_ID"
    exit 1
fi

echo "📋 Using GCP Project: $PROJECT_ID"

# Create static directory
echo "📁 Creating static directory..."
mkdir -p static

# Copy static files from parent directories
echo "📄 Copying static files..."
if [[ -d "../../static" ]]; then
    cp -r ../../static/* static/
    echo "✅ Copied files from ../../static/"
elif [[ -d "../../dashboard" ]]; then
    cp -r ../../dashboard/* static/
    echo "✅ Copied files from ../../dashboard/"
else
    echo "⚠️  No static files found in ../../static or ../../dashboard"
    echo "   Please manually copy your dashboard files to the static/ directory"
fi

# Update environment files with actual project ID
echo "🔧 Updating environment files with project ID..."
sed -i.bak "s/your-gcp-project/$PROJECT_ID/g" .env.dev.yaml .env.prod.yaml 2>/dev/null || true
rm -f .env.dev.yaml.bak .env.prod.yaml.bak 2>/dev/null || true

# Enable required APIs
echo "🔌 Enabling required GCP APIs..."
gcloud services enable cloudfunctions.googleapis.com
gcloud services enable spanner.googleapis.com
gcloud services enable container.googleapis.com
gcloud services enable monitoring.googleapis.com
gcloud services enable cloudbuild.googleapis.com

echo "✅ APIs enabled successfully"

# Run go mod tidy
echo "📦 Running go mod tidy..."
go mod tidy

echo ""
echo "🎉 Setup complete! Next steps:"
echo ""
echo "1. Run the full setup:"
echo "   make setup PROJECT_ID=$PROJECT_ID"
echo ""
echo "2. Deploy to development:"
echo "   make deploy-dev PROJECT_ID=$PROJECT_ID"
echo ""
echo "3. Test the deployment:"
echo "   make test-real PROJECT_ID=$PROJECT_ID"
echo ""
echo "4. Get the function URL:"
echo "   make get-url-dev PROJECT_ID=$PROJECT_ID"
echo ""
echo "For more options, run: make help"
echo ""
echo "📚 Check the setup guide for detailed instructions and troubleshooting."
EOF
	@chmod +x setup.sh

# Help target
.PHONY: help
help:
	@echo "Highlander Cloud Function with Spanner - Available targets:"
	@echo ""
	@echo "📦 Build & Test:"
	@echo "  all              : Run tests, lint, and build for Cloud Function (default)"
	@echo "  build-cf         : Build for Cloud Function"
	@echo "  test             : Run unit tests"
	@echo "  lint             : Run linter"
	@echo "  clean            : Clean build artifacts"
	@echo ""
	@echo "🚀 Setup (Real Spanner):"
	@echo "  setup            : Full setup with real Spanner (costs money!)"
	@echo "  enable-apis      : Enable required GCP APIs"
	@echo "  setup-service-account : Create service account with Spanner permissions"
	@echo "  setup-spanner    : Create real Spanner instance and database"
	@echo "  create-env-files : Create environment configuration files"
	@echo "  create-schema    : Create database schema file"
	@echo ""
	@echo "🧪 Emulator Testing:"
	@echo "  setup-emulator   : Setup and start Spanner emulator"
	@echo "  test-emulator    : Test with Spanner emulator (free)"
	@echo "  stop-emulator    : Stop Spanner emulator"
	@echo ""
	@echo "🚀 Deployment:"
	@echo "  deploy-dev       : Deploy to development environment"
	@echo "  deploy-prod      : Deploy to production environment"
	@echo "  deploy-with-vpc  : Deploy with VPC connector for GKE access"
	@echo ""
	@echo "🔍 Testing:"
	@echo "  test-real        : Test deployed function with real Spanner"
	@echo "  test-dev         : Test development deployment"
	@echo ""
	@echo "📊 Management:"
	@echo "  logs             : View production function logs"
	@echo "  logs-dev         : View development function logs"
	@echo "  get-url          : Get production function URL"
	@echo "  get-url-dev      : Get development function URL"
	@echo "  check-costs      : Check Spanner instance costs"
	@echo ""
	@echo "🗑️  Cleanup:"
	@echo "  delete           : Delete production function"
	@echo "  delete-dev       : Delete development function"
	@echo "  cleanup-spanner  : Delete Spanner resources (with confirmation)"
	@echo "  cleanup-all      : Delete ALL resources (with confirmation)"
	@echo ""
	@echo "🛠️  Utilities:"
	@echo "  create-setup-script : Create setup.sh script"
	@echo "  prepare-static   : Copy static files for embedding"
	@echo ""
	@echo "💡 Quick Start:"
	@echo "  1. make test-emulator           # Free testing with emulator"
	@echo "  2. make setup                   # Setup real Spanner (costs money)"
	@echo "  3. make deploy-dev             # Deploy to development"
	@echo "  4. make test-real              # Test the deployment"
	@echo ""
	@echo "  Variables you can override:"
	@echo "    PROJECT_ID=$(PROJECT_ID)"
	@echo "    SPANNER_INSTANCE_ID=$(SPANNER_INSTANCE_ID)"
	@echo "    SPANNER_DATABASE_ID=$(SPANNER_DATABASE_ID)"
