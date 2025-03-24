#!/bin/bash

# Script to list all pods in a specified Kubernetes namespace
# Default namespace is "default" if not specified

# kubectl get pods --all-namespaces -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,CPU_REQUEST:.spec.containers[*].resources.requests.cpu,MEMORY_REQUEST:.spec.containers[*].resources.requests.memory

# Function to display usage
function show_usage {
  echo "Usage: $0 [-n NAMESPACE] [-o OUTPUT_FORMAT] [-w WATCH]"
  echo "  -n NAMESPACE      Kubernetes namespace (default: default)"
  echo "  -o OUTPUT_FORMAT  Output format (wide|json|yaml) (default: wide)"
  echo "  -w                Watch for changes"
  echo "  -h                Display this help message"
  echo
  echo "Examples:"
  echo "  $0                        # List pods in default namespace"
  echo "  $0 -n kube-system         # List pods in kube-system namespace"
  echo "  $0 -n myapp -o json       # List pods in myapp namespace in JSON format"
  echo "  $0 -n myapp -w            # Watch pods in myapp namespace"
}

# Default values
NAMESPACE="default"
OUTPUT_FORMAT="wide"
WATCH=""

# Parse command line arguments
while getopts "n:o:wh" opt; do
  case $opt in
    n)
      NAMESPACE="$OPTARG"
      ;;
    o)
      OUTPUT_FORMAT="$OPTARG"
      ;;
    w)
      WATCH="--watch"
      ;;
    h)
      show_usage
      exit 0
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      show_usage
      exit 1
      ;;
  esac
done

# Check if kubectl is installed
if ! command -v kubectl &> /dev/null; then
  echo "Error: kubectl command not found."
  echo "Please install kubectl before running this script."
  exit 1
fi

# Check if the namespace exists
if ! kubectl get namespace "$NAMESPACE" &> /dev/null; then
  echo "Error: Namespace '$NAMESPACE' does not exist."
  echo "Available namespaces:"
  kubectl get namespaces | grep -v "NAME" | awk '{print "  " $1}'
  exit 1
fi

# Set column colors
HEADER='\033[1;36m'  # Cyan bold
STATUS_RUNNING='\033[0;32m'  # Green
STATUS_PENDING='\033[0;33m'  # Yellow
STATUS_ERROR='\033[0;31m'    # Red
STATUS_COMPLETED='\033[0;34m' # Blue
NC='\033[0m'  # No Color

echo -e "${HEADER}Listing pods in namespace: ${NAMESPACE}${NC}"

# Get current date/time
TIMESTAMP=$(date +"%Y-%m-%d %H:%M:%S")
echo -e "${HEADER}Time: ${TIMESTAMP}${NC}"
echo

# Function to colorize pod status
colorize_output() {
  if [ "$OUTPUT_FORMAT" == "wide" ]; then
    awk '
    BEGIN {FS=OFS="  "}
    NR==1 {print "\033[1;36m" $0 "\033[0m"; next}
    $3=="Running" {gsub("Running", "\033[0;32mRunning\033[0m", $0)}
    $3=="Pending" {gsub("Pending", "\033[0;33mPending\033[0m", $0)}
    $3=="Succeeded" {gsub("Succeeded", "\033[0;34mSucceeded\033[0m", $0)}
    $3=="Failed" {gsub("Failed", "\033[0;31mFailed\033[0m", $0)}
    $3=="CrashLoopBackOff" {gsub("CrashLoopBackOff", "\033[0;31mCrashLoopBackOff\033[0m", $0)}
    {print}'
  else
    cat  # Don't colorize non-wide output formats
  fi
}

# Prepare additional flags based on output format
FORMAT_FLAG=""
case "$OUTPUT_FORMAT" in
  json)
    FORMAT_FLAG="-o json"
    ;;
  yaml)
    FORMAT_FLAG="-o yaml"
    ;;
  wide)
    FORMAT_FLAG="-o wide"
    ;;
  *)
    echo "Warning: Unknown output format '$OUTPUT_FORMAT'. Using 'wide' instead."
    FORMAT_FLAG="-o wide"
    ;;
esac

# Execute the command to list pods
kubectl get pods -n "$NAMESPACE" $FORMAT_FLAG $WATCH | colorize_output

# Show pods count if not in watch mode
if [ -z "$WATCH" ]; then
  POD_COUNT=$(kubectl get pods -n "$NAMESPACE" --no-headers | wc -l)
  echo -e "\n${HEADER}Total pods: ${POD_COUNT}${NC}"
fi

exit 0
