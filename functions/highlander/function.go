package highlander

import (
	"encoding/json"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/server"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"github.com/bhatti/k8-highlander/pkg/service"
	"github.com/bhatti/k8-highlander/pkg/storage"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

var (
	highlanderService *service.HighlanderService
	initOnce          sync.Once
	initError         error
)

// init registers the Cloud Function
func init() {
	functions.HTTP("HighlanderAPI", HighlanderAPI)
}

// HighlanderAPI is the main Cloud Function entry point
func HighlanderAPI(w http.ResponseWriter, r *http.Request) {
	// Initialize service on first request
	initOnce.Do(func() {
		initError = initializeService()
	})

	if initError != nil {
		http.Error(w, fmt.Sprintf("Service initialization failed: %v", initError), http.StatusInternalServerError)
		return
	}

	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight OPTIONS requests
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Route the request
	routeRequest(w, r)
}

// initializeService initializes the highlander service
func initializeService() error {
	// Load configuration from environment variables
	config, err := loadConfigFromEnv()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize storage
	storageConfig, err := config.BuildStorageConfig()
	if err != nil {
		return fmt.Errorf("failed to build storage config: %w", err)
	}

	leaderStorage, err := storage.NewLeaderStorage(storageConfig)
	if err != nil {
		return fmt.Errorf("failed to create leader storage: %w", err)
	}

	// Initialize monitoring server
	metrics := monitoring.NewControllerMetrics(nil) // Use default registry
	monitoringServer := monitoring.NewMonitoringServer(
		metrics,
		common.VERSION,
		common.BuildInfo,
	)

	// Create the service
	highlanderService = service.NewHighlanderService(config, monitoringServer)

	// Initialize Kubernetes client if running in cluster
	if err := initializeKubernetesClient(config); err != nil {
		klog.Warningf("Failed to initialize Kubernetes client: %v", err)
		// Don't fail the entire service if K8s client fails
	}

	// Initialize workload manager if possible
	if err := initializeWorkloadManager(config, leaderStorage); err != nil {
		klog.Warningf("Failed to initialize workload manager: %v", err)
		// Don't fail the entire service if workload manager fails
	}

	klog.Infof("Highlander service initialized successfully")
	return nil
}

// loadConfigFromEnv loads configuration from environment variables
func loadConfigFromEnv() (*common.AppConfig, error) {
	config := &common.AppConfig{
		ID:          getEnvOrDefault("HIGHLANDER_ID", "cloud-function"),
		Tenant:      getEnvOrDefault("HIGHLANDER_TENANT", "default"),
		Namespace:   getEnvOrDefault("HIGHLANDER_NAMESPACE", "default"),
		Port:        8080,                                                                      // Not used in Cloud Functions but kept for compatibility
		StorageType: common.StorageType(getEnvOrDefault("HIGHLANDER_STORAGE_TYPE", "spanner")), // Default to Spanner for CF
	}

	// Load Redis configuration (fallback option)
	config.Redis.Addr = getEnvOrDefault("HIGHLANDER_REDIS_ADDR", "localhost:6379")
	config.Redis.Password = os.Getenv("HIGHLANDER_REDIS_PASSWORD")

	// Load Database configuration (fallback option)
	config.DatabaseURL = os.Getenv("HIGHLANDER_DATABASE_URL")

	// Load Spanner configuration (primary for Cloud Functions)
	config.Spanner.ProjectID = getEnvOrDefault("HIGHLANDER_SPANNER_PROJECT_ID", os.Getenv("GOOGLE_CLOUD_PROJECT"))
	config.Spanner.InstanceID = getEnvOrDefault("HIGHLANDER_SPANNER_INSTANCE_ID", "highlander-instance")
	config.Spanner.DatabaseID = getEnvOrDefault("HIGHLANDER_SPANNER_DATABASE_ID", "highlander-db")

	// Load cluster configuration
	config.Cluster.Name = getEnvOrDefault("HIGHLANDER_CLUSTER_NAME", "default")
	config.Cluster.Kubeconfig = os.Getenv("HIGHLANDER_KUBECONFIG")

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// initializeKubernetesClient initializes the Kubernetes client
func initializeKubernetesClient(config *common.AppConfig) error {
	// Try in-cluster config first (for GKE)
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		klog.Warningf("Failed to get in-cluster config (this is normal for Cloud Functions outside GKE): %v", err)
		return err
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	highlanderService.SetKubernetesClient(clientset, config.Namespace)
	klog.Infof("Kubernetes client initialized successfully")
	return nil
}

// initializeWorkloadManager initializes the workload manager
func initializeWorkloadManager(config *common.AppConfig, leaderStorage storage.LeaderStorage) error {
	// This would need to be adapted based on your workload manager implementation
	// For now, we'll skip this or implement a minimal version
	klog.Infof("Workload manager initialization skipped in Cloud Function mode")
	return nil
}

// routeRequest routes HTTP requests to appropriate handlers
func routeRequest(w http.ResponseWriter, r *http.Request) {
	// Parse the path
	urlPath := strings.TrimPrefix(r.URL.Path, "/")
	pathParts := strings.Split(urlPath, "/")

	// Set content type for JSON responses
	w.Header().Set("Content-Type", "application/json")

	switch {
	case urlPath == "" || urlPath == "healthz":
		handleHealthz(w, r)

	case urlPath == "readyz":
		handleReadyz(w, r)

	case urlPath == "status" || urlPath == "api/status":
		handleStatus(w, r)

	case urlPath == "debug/status":
		handleDebugStatus(w, r)

	case urlPath == "api/dbinfo":
		handleDBInfo(w, r)

	case urlPath == "api/workloads":
		handleWorkloads(w, r)

	case urlPath == "api/workload-names":
		handleWorkloadNames(w, r)

	case len(pathParts) >= 3 && pathParts[0] == "api" && pathParts[1] == "workloads":
		handleSpecificWorkload(w, r, pathParts[2])

	case urlPath == "api/pods":
		handlePods(w, r)

	case urlPath == "metrics":
		handleMetrics(w, r)

	default:
		// For static files or unknown paths, return the dashboard
		if strings.HasPrefix(urlPath, "api/") {
			http.NotFound(w, r)
		} else {
			server.HandleDashboard(w, r)
		}
	}
}

// HTTP handlers

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func handleReadyz(w http.ResponseWriter, r *http.Request) {
	ready, reason := highlanderService.GetReadinessStatus()
	if ready {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("not ready - " + reason))
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	response := highlanderService.GetHealthStatus()
	json.NewEncoder(w).Encode(response)
}

func handleDebugStatus(w http.ResponseWriter, r *http.Request) {
	response := highlanderService.GetDebugStatus()
	json.NewEncoder(w).Encode(response)
}

func handleDBInfo(w http.ResponseWriter, r *http.Request) {
	response := highlanderService.GetDBInfo()
	json.NewEncoder(w).Encode(response)
}

func handleWorkloads(w http.ResponseWriter, r *http.Request) {
	response := highlanderService.GetWorkloads()
	json.NewEncoder(w).Encode(response)
}

func handleWorkloadNames(w http.ResponseWriter, r *http.Request) {
	response := highlanderService.GetWorkloadNames()
	json.NewEncoder(w).Encode(response)
}

func handleSpecificWorkload(w http.ResponseWriter, r *http.Request, workloadName string) {
	response := highlanderService.GetWorkload(workloadName)
	if response.Status == "error" {
		if response.Error == "workload not found" {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
	json.NewEncoder(w).Encode(response)
}

func handlePods(w http.ResponseWriter, r *http.Request) {
	response := highlanderService.GetPods(r.Context())
	if response.Status == "error" {
		w.WriteHeader(http.StatusInternalServerError)
	}
	json.NewEncoder(w).Encode(response)
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	// For Cloud Functions, we might want to implement a simplified metrics endpoint
	// or redirect to Cloud Monitoring
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("# Metrics not available in Cloud Function mode\n"))
}

// Utility functions

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
