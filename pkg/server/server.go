// MIT License
//
// Copyright (c) 2024 PlexObject Solutions, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Package server implements the HTTP server and API endpoints for k8-highlander.
//
// The server provides:
//  - Health check endpoints for liveness and readiness probes
//  - Status reporting for the controller and workloads
//  - Debug and monitoring information
//  - Prometheus metrics
//  - RESTful API for workload management
//  - Static file serving for the dashboard UI
//
// The server is designed to work with Kubernetes health probes and provides
// appropriate status codes based on the controller's leadership status
// and overall system health.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/workloads"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"net/http"
	"runtime"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/bhatti/k8-highlander/pkg/monitoring"
)

// Server represents the HTTP server for the controller
type Server struct {
	server           *http.Server
	monitoringServer *monitoring.MonitoringServer
	config           *common.AppConfig
	port             int
	startTime        time.Time
}

// DBInfo contains information about the database connection
type DBInfo struct {
	Type      string            `json:"type"`
	Address   string            `json:"address"`
	Connected bool              `json:"connected"`
	Details   map[string]string `json:"details"`
}

// NewServer creates a new HTTP server
func NewServer(config *common.AppConfig, monitoringServer *monitoring.MonitoringServer) *Server {
	return &Server{
		config:           config,
		port:             config.Port,
		monitoringServer: monitoringServer,
		startTime:        time.Now(),
	}
}

// Start starts the HTTP server
func (s *Server) Start(_ context.Context, workloadManager workloads.Manager,
	k8sClient kubernetes.Interface, namespace string) error {
	mux := http.NewServeMux()

	// Add basic status endpoints
	mux.HandleFunc("/healthz", s.healthzHandler)
	mux.HandleFunc("/readyz", s.readyzHandler)
	mux.HandleFunc("/status", s.statusHandler)
	mux.HandleFunc("/debug/status", s.debugStatusHandler)
	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/api/dbinfo", s.dbInfoHandler)

	// Add dashboard endpoint
	// mux.HandleFunc("/", s.dashboardHandler)

	// Add Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Add workload API endpoints
	if workloadManager != nil {
		s.SetupWorkloadEndpoints(mux, workloadManager)
	}

	// Add pod API endpoints
	if k8sClient != nil {
		s.SetupPodEndpoints(mux, k8sClient, namespace)
	}

	// Set up static file server (this replaces the old dashboardHandler)
	s.setupStaticFileServer(mux)

	// Create the server
	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	// Use a channel to communicate errors from the goroutine
	errChan := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		klog.Infof("Starting HTTP server on %s", s.server.Addr)
		err := s.server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			klog.Errorf("HTTP server failed: %v", err)
			errChan <- err
		}
		close(errChan)
	}()

	// Wait a short time to see if the server starts successfully
	select {
	case err := <-errChan:
		// Server failed to start
		return fmt.Errorf("failed to start HTTP server: %w", err)
	case <-time.After(500 * time.Millisecond):
		// Server started successfully (or at least didn't fail immediately)
		// Try to connect to the server to verify it's running
		client := &http.Client{
			Timeout: 500 * time.Millisecond,
		}
		_, err := client.Get(fmt.Sprintf("http://localhost:%d/healthz", s.port))
		if err != nil {
			// Check if there's an error in the channel
			select {
			case err := <-errChan:
				if err != nil {
					return fmt.Errorf("server failed to start: %w", err)
				}
			default:
				// No error in channel, but connection failed
				return fmt.Errorf("server started but health check failed: %w", err)
			}
		}

		// Server is running
		return nil
	}
}

// Stop stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// healthHandler handles health check requests
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	// Delegate to the monitoring server's health handler if available
	if s.monitoringServer != nil {
		status := s.monitoringServer.GetHealthStatus()

		// Only return ready if we're the leader or a stable follower
		if status.ReadyForTraffic {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
		}
		return
	}

	// Fallback implementation
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// DashboardHandler serves the dashboard UI and related static files
//func (s *Server) dashboardHandler(w http.ResponseWriter, r *http.Request) {
//	// Check if we're requesting a static file
//	if strings.HasSuffix(r.URL.Path, ".js") ||
//		strings.HasSuffix(r.URL.Path, ".css") ||
//		strings.HasSuffix(r.URL.Path, ".html") {
//		s.serveStaticFile(w, r)
//		return
//	}
//
//	// Serve the main dashboard HTML
//	s.serveStaticFile(w, r.WithContext(r.Context()))
//}

// serveStaticFile serves static files for the dashboard
//func (s *Server) serveStaticFile(w http.ResponseWriter, r *http.Request) {
//	// Default to serving index.html for the root path
//	path := r.URL.Path
//	if path == "/" {
//		path = "/dashboard.html"
//	}
//
//	// Remove leading slash
//	path = strings.TrimPrefix(path, "/")
//
//	// Look for the file in the static directory
//	filePath := filepath.Join("static", path)
//
//	// Check if the file exists
//	_, err := os.Stat(filePath)
//	if os.IsNotExist(err) {
//		klog.Warningf("Static file not found: %s", filePath)
//		http.NotFound(w, r)
//		return
//	}
//
//	// Serve the file
//	http.ServeFile(w, r, filePath)
//}

// SetupWorkloadEndpoints adds workload-related API endpoints to the HTTP server
func (s *Server) SetupWorkloadEndpoints(mux *http.ServeMux, workloadManager workloads.Manager) {
	// Add endpoint for all workloads status
	mux.HandleFunc("/api/workloads", func(w http.ResponseWriter, r *http.Request) {
		statuses := workloadManager.GetAllStatuses()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   statuses,
		})
	})

	// Add endpoint for workload names
	mux.HandleFunc("/api/workload-names", func(w http.ResponseWriter, r *http.Request) {
		workloads := workloadManager.GetAllWorkloads()
		names := make([]string, 0, len(workloads))
		for name := range workloads {
			names = append(names, name)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   names,
		})
	})

	// Add endpoint for specific workload
	mux.HandleFunc("/api/workloads/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 4 {
			http.Error(w, "Invalid workload path", http.StatusBadRequest)
			return
		}

		workloadName := parts[3]
		workload, exists := workloadManager.GetWorkload(workloadName)
		if !exists {
			http.Error(w, "Workload not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   workload.GetStatus(),
		})
	})

	// Add endpoint for controller status
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		s.statusHandler(w, r)
	})

	klog.Infof("Workload API endpoints configured")
}

// healthzHandler handles liveness probe requests
func (s *Server) healthzHandler(w http.ResponseWriter, _ *http.Request) {
	// Basic liveness check - if the server is responding, it's alive
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyzHandler handles readiness probe requests
func (s *Server) readyzHandler(w http.ResponseWriter, _ *http.Request) {
	if s.monitoringServer != nil {
		status := s.monitoringServer.GetHealthStatus()

		// Only return ready if we're the leader or a stable follower
		if status.ReadyForTraffic {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready - " + s.determineNotReadyReason(status)))
		}
		return
	}

	// Fallback implementation
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// determineNotReadyReason returns a reason why the service is not ready
func (s *Server) determineNotReadyReason(status monitoring.HealthStatus) string {
	if !status.IsLeader && time.Since(status.LastLeaderTransition) < s.config.TerminationGracePeriod {
		return "recent leadership change" +
			""
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

// statusHandler returns the current status as JSON
func (s *Server) statusHandler(w http.ResponseWriter, _ *http.Request) {
	if s.monitoringServer != nil {
		status := s.monitoringServer.GetHealthStatus()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
		return
	}

	// Fallback implementation
	status := map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// debugStatusHandler returns detailed debug information
func (s *Server) debugStatusHandler(w http.ResponseWriter, _ *http.Request) {
	if s.monitoringServer != nil {
		status := s.monitoringServer.GetHealthStatus()

		// Add additional debug information
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		debugInfo := struct {
			HealthStatus   interface{}       `json:"healthStatus"`
			Uptime         time.Duration     `json:"uptime"`
			StartTime      time.Time         `json:"startTime"`
			GoroutineCount int               `json:"goroutineCount"`
			MemoryStats    map[string]uint64 `json:"memoryStats"`
			ServerInfo     map[string]string `json:"serverInfo"`
		}{
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
				"port":   fmt.Sprintf("%d", s.port),
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(debugInfo)
		return
	}

	// Fallback implementation
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// dbInfoHandler returns information about the database connection
func (s *Server) dbInfoHandler(w http.ResponseWriter, r *http.Request) {
	dbInfo := s.getDBInfo()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data":   dbInfo,
	})
}

func (s *Server) getDBInfo() DBInfo {
	// Get app config to determine storage type
	storageType := s.config.StorageType

	dbInfo := DBInfo{
		Type:    string(storageType),
		Details: make(map[string]string),
	}

	// Set address based on storage type
	switch storageType {
	case "redis":
		dbInfo.Address = s.config.Redis.Addr
		// Check if Redis is connected via the monitoring server
		if s.monitoringServer != nil {
			status := s.monitoringServer.GetHealthStatus()
			dbInfo.Connected = status.DBConnected
		}

	case "db":
		dbInfo.Address = maskDatabasePassword(s.config.DatabaseURL)

		// For database, we don't have a direct connection check
		// but we can use the monitoring server's status
		if s.monitoringServer != nil {
			status := s.monitoringServer.GetHealthStatus()
			dbInfo.Connected = status.DBConnected // You'll need to add this field to HealthStatus
		}

		if strings.Contains(dbInfo.Address, "postgres") {
			dbInfo.Details["type"] = "PostgreSQL"
		} else if strings.Contains(dbInfo.Address, "mysql") {
			dbInfo.Details["type"] = "MySQL"
		} else if strings.Contains(dbInfo.Address, "sqlite") {
			dbInfo.Details["type"] = "SQLite"
		}
	}
	return dbInfo
}

// maskDatabasePassword masks the password in a database URL
func maskDatabasePassword(url string) string {
	// Handle postgres URLs like postgres://username:password@hostname:port/database
	if strings.Contains(url, "://") && strings.Contains(url, "@") {
		parts := strings.SplitN(url, "@", 2)
		if len(parts) == 2 {
			credentialsPart := parts[0]
			hostPart := parts[1]

			// Split credentials into protocol and user:pass
			credentialsSplit := strings.SplitN(credentialsPart, "://", 2)
			if len(credentialsSplit) == 2 {
				protocol := credentialsSplit[0]
				userPass := credentialsSplit[1]

				// Split user:pass
				userPassSplit := strings.SplitN(userPass, ":", 2)
				if len(userPassSplit) == 2 {
					user := userPassSplit[0]
					// Mask the password
					return fmt.Sprintf("%s://%s:****@%s", protocol, user, hostPart)
				}
			}
		}
	}

	// For SQLite or other formats, just return as is
	return url
}
