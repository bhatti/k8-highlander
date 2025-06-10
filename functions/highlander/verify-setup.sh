#!/bin/bash
# verify-setup.sh - Verify Spanner and Cloud Function setup

set -e

PROJECT_ID=${PROJECT_ID:-$(gcloud config get-value project)}
SERVICE_ACCOUNT_NAME=${SERVICE_ACCOUNT_NAME:-highlander-cf}
SERVICE_ACCOUNT_EMAIL=${SERVICE_ACCOUNT_NAME}@${PROJECT_ID}.iam.gserviceaccount.com
SPANNER_INSTANCE_ID=${SPANNER_INSTANCE_ID:-highlander-instance}
SPANNER_DATABASE_ID=${SPANNER_DATABASE_ID:-highlander-db}
REGION=${REGION:-us-central1}

echo "🔍 Verifying Highlander Cloud Function Setup"
echo "============================================"
echo "Project: $PROJECT_ID"
echo "Service Account: $SERVICE_ACCOUNT_EMAIL"
echo "Spanner Instance: $SPANNER_INSTANCE_ID"
echo "Spanner Database: $SPANNER_DATABASE_ID"
echo ""

# Check if gcloud is authenticated
echo "🔐 Checking gcloud authentication..."
if gcloud auth list --filter=status:ACTIVE --format="value(account)" | head -1 >/dev/null; then
    ACTIVE_ACCOUNT=$(gcloud auth list --filter=status:ACTIVE --format="value(account)" | head -1)
    echo "✅ Authenticated as: $ACTIVE_ACCOUNT"
else
    echo "❌ Not authenticated. Run: gcloud auth login"
    exit 1
fi

# Check project access
echo "🏗️  Checking project access..."
if gcloud projects describe $PROJECT_ID >/dev/null 2>&1; then
    PROJECT_NAME=$(gcloud projects describe $PROJECT_ID --format="value(name)")
    echo "✅ Project access confirmed: $PROJECT_NAME"
else
    echo "❌ Cannot access project $PROJECT_ID"
    exit 1
fi

# Check required APIs
echo "🔌 Checking required APIs..."
REQUIRED_APIS=(
    "cloudfunctions.googleapis.com"
    "spanner.googleapis.com"
    "container.googleapis.com"
    "monitoring.googleapis.com"
    "cloudbuild.googleapis.com"
)

for api in "${REQUIRED_APIS[@]}"; do
    if gcloud services list --enabled --filter="name:$api" --format="value(name)" | grep -q "$api"; then
        echo "✅ $api is enabled"
    else
        echo "❌ $api is not enabled"
        echo "   Run: gcloud services enable $api"
        exit 1
    fi
done

# Check service account
echo "👤 Checking service account..."
if gcloud iam service-accounts describe $SERVICE_ACCOUNT_EMAIL --project=$PROJECT_ID >/dev/null 2>&1; then
    echo "✅ Service account exists: $SERVICE_ACCOUNT_EMAIL"

    # Check IAM roles
    echo "🔑 Checking IAM roles..."
    REQUIRED_ROLES=(
        "roles/spanner.databaseUser"
        "roles/container.viewer"
        "roles/monitoring.viewer"
    )

    for role in "${REQUIRED_ROLES[@]}"; do
        if gcloud projects get-iam-policy $PROJECT_ID \
            --flatten="bindings[].members" \
            --format="table(bindings.role)" \
            --filter="bindings.members:serviceAccount:$SERVICE_ACCOUNT_EMAIL AND bindings.role:$role" | grep -q "$role"; then
            echo "✅ $role assigned to service account"
        else
            echo "❌ $role not assigned to service account"
            echo "   Run: gcloud projects add-iam-policy-binding $PROJECT_ID --member=\"serviceAccount:$SERVICE_ACCOUNT_EMAIL\" --role=\"$role\""
        fi
    done
else
    echo "❌ Service account does not exist: $SERVICE_ACCOUNT_EMAIL"
    echo "   Run: make setup-service-account"
    exit 1
fi

# Check Spanner instance
echo "🗄️  Checking Spanner instance..."
if gcloud spanner instances describe $SPANNER_INSTANCE_ID --project=$PROJECT_ID >/dev/null 2>&1; then
    INSTANCE_INFO=$(gcloud spanner instances describe $SPANNER_INSTANCE_ID --project=$PROJECT_ID --format="value(config,nodeCount,processingUnits)")
    echo "✅ Spanner instance exists: $SPANNER_INSTANCE_ID"
    echo "   Config: $(echo $INSTANCE_INFO | cut -d' ' -f1)"
    echo "   Nodes: $(echo $INSTANCE_INFO | cut -d' ' -f2)"
    echo "   Processing Units: $(echo $INSTANCE_INFO | cut -d' ' -f3)"

    # Check Spanner database
    echo "💾 Checking Spanner database..."
    if gcloud spanner databases describe $SPANNER_DATABASE_ID --instance=$SPANNER_INSTANCE_ID --project=$PROJECT_ID >/dev/null 2>&1; then
        echo "✅ Spanner database exists: $SPANNER_DATABASE_ID"

        # Check database tables
        echo "📋 Checking database schema..."
        TABLES=$(gcloud spanner databases execute-sql $SPANNER_DATABASE_ID \
            --instance=$SPANNER_INSTANCE_ID \
            --project=$PROJECT_ID \
            --sql="SELECT table_name FROM information_schema.tables WHERE table_schema = ''" \
            --format="value(table_name)" 2>/dev/null || echo "")

        if echo "$TABLES" | grep -q "leaders"; then
            echo "✅ 'leaders' table exists"
        else
            echo "❌ 'leaders' table missing"
        fi

        if echo "$TABLES" | grep -q "workloads"; then
            echo "✅ 'workloads' table exists"
        else
            echo "❌ 'workloads' table missing"
        fi

        if [[ -z "$TABLES" ]]; then
            echo "⚠️  Could not verify tables (may need schema creation)"
            echo "   Run: make create-schema && make setup-spanner"
        fi

    else
        echo "❌ Spanner database does not exist: $SPANNER_DATABASE_ID"
        echo "   Run: make setup-spanner"
    fi
else
    echo "❌ Spanner instance does not exist: $SPANNER_INSTANCE_ID"
    echo "   Run: make setup-spanner"
fi

# Check if function is deployed
echo "⚡ Checking Cloud Function deployment..."
FUNCTION_DEPLOYED=false
for func_name in "highlander-api" "highlander-api-dev"; do
    if gcloud functions describe $func_name --region=$REGION --project=$PROJECT_ID >/dev/null 2>&1; then
        echo "✅ Function deployed: $func_name"
        FUNCTION_URL=$(gcloud functions describe $func_name --region=$REGION --project=$PROJECT_ID --format="value(serviceConfig.uri)")
        echo "   URL: $FUNCTION_URL"
        FUNCTION_DEPLOYED=true
    fi
done

if ! $FUNCTION_DEPLOYED; then
    echo "⚠️  No Cloud Function deployed"
    echo "   Run: make deploy-dev"
fi

# Test Spanner connectivity (if possible)
echo "🔗 Testing Spanner connectivity..."
if command -v gcloud >/dev/null && gcloud spanner databases execute-sql $SPANNER_DATABASE_ID \
    --instance=$SPANNER_INSTANCE_ID \
    --project=$PROJECT_ID \
    --sql="SELECT 1 as test" \
    --format="value(test)" >/dev/null 2>&1; then
    echo "✅ Spanner connectivity test passed"
else
    echo "⚠️  Spanner connectivity test failed (check permissions)"
fi

# Estimate costs
echo "💰 Estimating costs..."
if gcloud spanner instances describe $SPANNER_INSTANCE_ID --project=$PROJECT_ID >/dev/null 2>&1; then
    NODE_COUNT=$(gcloud spanner instances describe $SPANNER_INSTANCE_ID --project=$PROJECT_ID --format="value(nodeCount)")
    PROCESSING_UNITS=$(gcloud spanner instances describe $SPANNER_INSTANCE_ID --project=$PROJECT_ID --format="value(processingUnits)")

    if [[ -n "$NODE_COUNT" && "$NODE_COUNT" != "0" ]]; then
        echo "⚠️  Using node-based pricing: $NODE_COUNT nodes"
        echo "   Estimated cost: ~$$(( NODE_COUNT * 744 )) USD/month (approximate)"
    elif [[ -n "$PROCESSING_UNITS" && "$PROCESSING_UNITS" != "0" ]]; then
        echo "✅ Using processing unit pricing: $PROCESSING_UNITS PUs"
        echo "   Estimated cost: ~$$(( PROCESSING_UNITS * 744 / 1000 )) USD/month (approximate)"
    fi
    echo "   Note: Actual costs may vary. Check GCP billing for exact amounts."
fi

echo ""
echo "📊 Setup Summary"
echo "==============="

# Count checks
CHECKS_PASSED=0
TOTAL_CHECKS=0

# Re-run key checks for summary
echo "Essential components:"

((TOTAL_CHECKS++))
if gcloud iam service-accounts describe $SERVICE_ACCOUNT_EMAIL --project=$PROJECT_ID >/dev/null 2>&1; then
    echo "✅ Service Account"
    ((CHECKS_PASSED++))
else
    echo "❌ Service Account"
fi

((TOTAL_CHECKS++))
if gcloud spanner instances describe $SPANNER_INSTANCE_ID --project=$PROJECT_ID >/dev/null 2>&1; then
    echo "✅ Spanner Instance"
    ((CHECKS_PASSED++))
else
    echo "❌ Spanner Instance"
fi

((TOTAL_CHECKS++))
if gcloud spanner databases describe $SPANNER_DATABASE_ID --instance=$SPANNER_INSTANCE_ID --project=$PROJECT_ID >/dev/null 2>&1; then
    echo "✅ Spanner Database"
    ((CHECKS_PASSED++))
else
    echo "❌ Spanner Database"
fi

((TOTAL_CHECKS++))
if $FUNCTION_DEPLOYED; then
    echo "✅ Cloud Function"
    ((CHECKS_PASSED++))
else
    echo "⚠️  Cloud Function (optional)"
fi

echo ""
echo "Status: $CHECKS_PASSED/$TOTAL_CHECKS essential components ready"

if [[ $CHECKS_PASSED -eq $TOTAL_CHECKS ]]; then
    echo "🎉 Setup is complete! You're ready to use Highlander with Spanner."
    echo ""
    echo "Next steps:"
    echo "  • Test deployment: make test-real"
    echo "  • View logs: make logs-dev"
    echo "  • Monitor costs: make check-costs"
elif [[ $CHECKS_PASSED -ge 3 ]]; then
    echo "⚠️  Setup is mostly complete. Deploy function with: make deploy-dev"
else
    echo "❌ Setup is incomplete. Run: make setup"
fi

echo ""
echo "💡 Quick commands:"
echo "  • Complete setup: make setup"
echo "  • Test with emulator: make test-emulator"
echo "  • Deploy function: make deploy-dev"
echo "  • Test deployment: make test-real"
echo "  • Clean up everything: make cleanup-all"
