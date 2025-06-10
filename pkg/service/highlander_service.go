package service

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"github.com/bhatti/k8-highlander/pkg/workloads"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// HighlanderService provides common business logic for both HTTP server and Cloud Functions
type HighlanderService struct {
	config           *common.AppConfig
	monitoringServer *monitoring.MonitoringServer
	workloadManager  workloads.Manager
	k8sClient        kubernetes.Interface
	namespace        string
	startTime        time.Time
}

// ServiceResponse represents a standardized API response
type ServiceResponse struct {
	Status string      `json:"status"`
	Data   interface{} `json:"data,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// PodInfo represents simplified pod information
type PodInfo struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Status       string            `json:"status"`
	Node         string            `json:"node"`
	Age          string            `json:"age"`
	CreatedAt    time.Time         `json:"createdAt"`
	ControllerID string            `json:"controllerId"`
	ClusterName  string            `json:"clusterName"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
}

// DBInfo contains information about the database connection
type DBInfo struct {
	Type      string            `json:"type"`
	Address   string            `json:"address"`
	Connected bool              `json:"connected"`
	Details   map[string]string `json:"details"`
}

// DebugInfo contains detailed debug information
type DebugInfo struct {
	HealthStatus   interface{}       `json:"healthStatus"`
	Uptime         time.Duration     `json:"uptime"`
	StartTime      time.Time         `json:"startTime"`
	GoroutineCount int               `json:"goroutineCount"`
	MemoryStats    map[string]uint64 `json:"memoryStats"`
	ServerInfo     map[string]string `json:"serverInfo"`
}

// NewHighlanderService creates a new service instance
func NewHighlanderService(config *common.AppConfig, monitoringServer *monitoring.MonitoringServer) *HighlanderService {
	return &HighlanderService{
		config:           config,
		monitoringServer: monitoringServer,
		startTime:        time.Now(),
	}
}

// SetWorkloadManager sets the workload manager
func (s *HighlanderService) SetWorkloadManager(manager workloads.Manager) {
	s.workloadManager = manager
}

// SetKubernetesClient sets the Kubernetes client
func (s *HighlanderService) SetKubernetesClient(client kubernetes.Interface, namespace string) {
	s.k8sClient = client
	s.namespace = namespace
}

// GetHealthStatus returns health status
func (s *HighlanderService) GetHealthStatus() ServiceResponse {
	if s.monitoringServer != nil {
		status := s.monitoringServer.GetHealthStatus()
		return ServiceResponse{
			Status: "success",
			Data:   status,
		}
	}

	return ServiceResponse{
		Status: "success",
		Data: map[string]interface{}{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
		},
	}
}

// GetReadinessStatus returns readiness status
func (s *HighlanderService) GetReadinessStatus() (bool, string) {
	if s.monitoringServer != nil {
		status := s.monitoringServer.GetHealthStatus()
		if status.ReadyForTraffic {
			return true, "ok"
		}
		return false, s.determineNotReadyReason(status)
	}
	return true, "ok"
}

// GetDebugStatus returns detailed debug information
func (s *HighlanderService) GetDebugStatus() ServiceResponse {
	if s.monitoringServer != nil {
		status := s.monitoringServer.GetHealthStatus()

		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		debugInfo := DebugInfo{
			HealthStatus:   status,
			Uptime:         time.Since(s.startTime),
			StartTime:      s.startTime,
			GoroutineCount: runtime.NumGoroutine(),
			MemoryStats: map[string]uint64{
				"alloc":      memStats.Alloc,
				"totalAlloc": memStats.TotalAlloc,
				"sys":        memStats.Sys,
				"numGC":      uint64(memStats.NumGC),
			},
			ServerInfo: map[string]string{
				"dbInfo": s.getDBInfo().Address,
				"port":   fmt.Sprintf("%d", s.config.Port),
			},
		}

		return ServiceResponse{
			Status: "success",
			Data:   debugInfo,
		}
	}

	return ServiceResponse{
		Status: "success",
		Data: map[string]interface{}{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
		},
	}
}

// GetDBInfo returns database connection information
func (s *HighlanderService) GetDBInfo() ServiceResponse {
	dbInfo := s.getDBInfo()
	return ServiceResponse{
		Status: "success",
		Data:   dbInfo,
	}
}

// GetWorkloads returns all workload statuses
func (s *HighlanderService) GetWorkloads() ServiceResponse {
	if s.workloadManager == nil {
		return ServiceResponse{
			Status: "error",
			Error:  "workload manager not available",
		}
	}

	statuses := s.workloadManager.GetAllStatuses()
	return ServiceResponse{
		Status: "success",
		Data:   statuses,
	}
}

// GetWorkloadNames returns workload names
func (s *HighlanderService) GetWorkloadNames() ServiceResponse {
	if s.workloadManager == nil {
		return ServiceResponse{
			Status: "error",
			Error:  "workload manager not available",
		}
	}

	workloads := s.workloadManager.GetAllWorkloads()
	names := make([]string, 0, len(workloads))
	for name := range workloads {
		names = append(names, name)
	}

	return ServiceResponse{
		Status: "success",
		Data:   names,
	}
}

// GetWorkload returns status for a specific workload
func (s *HighlanderService) GetWorkload(workloadName string) ServiceResponse {
	if s.workloadManager == nil {
		return ServiceResponse{
			Status: "error",
			Error:  "workload manager not available",
		}
	}

	workload, exists := s.workloadManager.GetWorkload(workloadName)
	if !exists {
		return ServiceResponse{
			Status: "error",
			Error:  "workload not found",
		}
	}

	return ServiceResponse{
		Status: "success",
		Data:   workload.GetStatus(),
	}
}

// GetPods returns pod information
func (s *HighlanderService) GetPods(ctx context.Context) ServiceResponse {
	if s.k8sClient == nil {
		return ServiceResponse{
			Status: "error",
			Error:  "kubernetes client not available",
		}
	}

	pods, err := s.listPods(ctx)
	if err != nil {
		return ServiceResponse{
			Status: "error",
			Error:  fmt.Sprintf("failed to list pods: %v", err),
		}
	}

	return ServiceResponse{
		Status: "success",
		Data:   pods,
	}
}

// Private helper methods

func (s *HighlanderService) determineNotReadyReason(status monitoring.HealthStatus) string {
	if !status.IsLeader && time.Since(status.LastLeaderTransition) < s.config.TerminationGracePeriod {
		return "recent leadership change"
	}

	if !status.DBConnected {
		return "db connection issue"
	}

	if !status.ClusterHealth {
		return "cluster health issue"
	}

	if status.LastError != "" {
		return "error: " + status.LastError
	}

	return "unknown reason"
}

func (s *HighlanderService) getDBInfo() DBInfo {
	storageType := s.config.StorageType

	dbInfo := DBInfo{
		Type:    string(storageType),
		Details: make(map[string]string),
	}

	switch storageType {
	case "redis":
		dbInfo.Address = s.config.Redis.Addr
		if s.monitoringServer != nil {
			status := s.monitoringServer.GetHealthStatus()
			dbInfo.Connected = status.DBConnected
		}

	case "db":
		dbInfo.Address = s.maskDatabasePassword(s.config.DatabaseURL)
		if s.monitoringServer != nil {
			status := s.monitoringServer.GetHealthStatus()
			dbInfo.Connected = status.DBConnected
		}

		if strings.Contains(dbInfo.Address, "postgres") {
			dbInfo.Details["type"] = "PostgreSQL"
		} else if strings.Contains(dbInfo.Address, "mysql") {
			dbInfo.Details["type"] = "MySQL"
		} else if strings.Contains(dbInfo.Address, "sqlite") {
			dbInfo.Details["type"] = "SQLite"
		}

	case "spanner":
		dbInfo.Address = fmt.Sprintf("projects/%s/instances/%s/databases/%s",
			s.config.Spanner.ProjectID, s.config.Spanner.InstanceID, s.config.Spanner.DatabaseID)
		if s.monitoringServer != nil {
			status := s.monitoringServer.GetHealthStatus()
			dbInfo.Connected = status.DBConnected
		}
		dbInfo.Details["type"] = "Cloud Spanner"
	}

	return dbInfo
}

func (s *HighlanderService) maskDatabasePassword(url string) string {
	if strings.Contains(url, "://") && strings.Contains(url, "@") {
		parts := strings.SplitN(url, "@", 2)
		if len(parts) == 2 {
			credentialsPart := parts[0]
			hostPart := parts[1]

			credentialsSplit := strings.SplitN(credentialsPart, "://", 2)
			if len(credentialsSplit) == 2 {
				protocol := credentialsSplit[0]
				userPass := credentialsSplit[1]

				userPassSplit := strings.SplitN(userPass, ":", 2)
				if len(userPassSplit) == 2 {
					user := userPassSplit[0]
					return fmt.Sprintf("%s://%s:****@%s", protocol, user, hostPart)
				}
			}
		}
	}
	return url
}

func (s *HighlanderService) listPods(ctx context.Context) ([]PodInfo, error) {
	if s.k8sClient == nil {
		return nil, nil
	}

	podList, err := s.k8sClient.CoreV1().Pods(s.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var pods []PodInfo
	for _, pod := range podList.Items {
		age := time.Since(pod.CreationTimestamp.Time)

		leaderID := ""
		clusterName := ""

		if pod.Annotations != nil {
			leaderID = pod.Annotations["k8-highlander.io/leader-id"]
			clusterName = pod.Annotations["k8-highlander.io/cluster-name"]
		}

		if leaderID == "" && pod.Labels != nil {
			leaderID = pod.Labels["controller-id"]
		}

		if clusterName == "" && pod.Labels != nil {
			clusterName = pod.Labels["cluster-name"]
		}

		pods = append(pods, PodInfo{
			Name:         pod.Name,
			Namespace:    pod.Namespace,
			Status:       string(pod.Status.Phase),
			Node:         pod.Spec.NodeName,
			Age:          s.formatDuration(age),
			CreatedAt:    pod.CreationTimestamp.Time,
			ControllerID: leaderID,
			ClusterName:  clusterName,
			Labels:       pod.Labels,
			Annotations:  pod.Annotations,
		})
	}

	return pods, nil
}

func (s *HighlanderService) formatDuration(d time.Duration) string {
	d = d.Round(time.Second)

	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour

	hours := d / time.Hour
	d -= hours * time.Hour

	minutes := d / time.Minute
	d -= minutes * time.Minute

	seconds := d / time.Second

	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
