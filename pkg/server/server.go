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
	"net/http"
	"strings"
	"time"

	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"github.com/bhatti/k8-highlander/pkg/service"
	"github.com/bhatti/k8-highlander/pkg/workloads"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// Server represents the HTTP server for the controller
type Server struct {
	server            *http.Server
	highlanderService *service.HighlanderService
	port              int
	startTime         time.Time
}

// NewServer creates a new HTTP server using the service layer
func NewServer(config *common.AppConfig, monitoringServer *monitoring.MonitoringServer) *Server {
	highlanderService := service.NewHighlanderService(config, monitoringServer)

	return &Server{
		highlanderService: highlanderService,
		port:              config.Port,
		startTime:         time.Now(),
	}
}

// Start starts the HTTP server
func (s *Server) Start(_ context.Context, workloadManager workloads.Manager,
	k8sClient kubernetes.Interface, namespace string) error {

	// Set dependencies on the service
	if workloadManager != nil {
		s.highlanderService.SetWorkloadManager(workloadManager)
	}
	if k8sClient != nil {
		s.highlanderService.SetKubernetesClient(k8sClient, namespace)
	}

	mux := http.NewServeMux()

	// Add basic status endpoints
	mux.HandleFunc("/healthz", s.healthzHandler)
	mux.HandleFunc("/readyz", s.readyzHandler)
	mux.HandleFunc("/status", s.statusHandler)
	mux.HandleFunc("/debug/status", s.debugStatusHandler)
	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/api/dbinfo", s.dbInfoHandler)

	// Add Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Add workload API endpoints
	s.SetupWorkloadEndpoints(mux)

	// Add pod API endpoints
	s.SetupPodEndpoints(mux)

	// Set up static file server
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
		return fmt.Errorf("failed to start HTTP server: %w", err)
	case <-time.After(500 * time.Millisecond):
		// Verify server is running
		client := &http.Client{Timeout: 500 * time.Millisecond}
		_, err := client.Get(fmt.Sprintf("http://localhost:%d/healthz", s.port))
		if err != nil {
			select {
			case err := <-errChan:
				if err != nil {
					return fmt.Errorf("server failed to start: %w", err)
				}
			default:
				return fmt.Errorf("server started but health check failed: %w", err)
			}
		}
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

// HTTP handlers using the service layer

func (s *Server) healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) readyzHandler(w http.ResponseWriter, _ *http.Request) {
	ready, reason := s.highlanderService.GetReadinessStatus()
	if ready {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready - " + reason))
	}
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	ready, reason := s.highlanderService.GetReadinessStatus()
	if ready {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready - " + reason))
	}
}

func (s *Server) statusHandler(w http.ResponseWriter, _ *http.Request) {
	response := s.highlanderService.GetHealthStatus()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) debugStatusHandler(w http.ResponseWriter, _ *http.Request) {
	response := s.highlanderService.GetDebugStatus()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) dbInfoHandler(w http.ResponseWriter, r *http.Request) {
	response := s.highlanderService.GetDBInfo()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// SetupWorkloadEndpoints adds workload-related API endpoints
func (s *Server) SetupWorkloadEndpoints(mux *http.ServeMux) {
	// All workloads status
	mux.HandleFunc("/api/workloads", func(w http.ResponseWriter, r *http.Request) {
		response := s.highlanderService.GetWorkloads()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})

	// Workload names
	mux.HandleFunc("/api/workload-names", func(w http.ResponseWriter, r *http.Request) {
		response := s.highlanderService.GetWorkloadNames()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})

	// Specific workload
	mux.HandleFunc("/api/workloads/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 4 {
			http.Error(w, "Invalid workload path", http.StatusBadRequest)
			return
		}

		workloadName := parts[3]
		response := s.highlanderService.GetWorkload(workloadName)

		w.Header().Set("Content-Type", "application/json")
		if response.Status == "error" {
			if response.Error == "workload not found" {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}
		_ = json.NewEncoder(w).Encode(response)
	})

	// Controller status
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		s.statusHandler(w, r)
	})

	klog.Infof("Workload API endpoints configured")
}

// SetupPodEndpoints adds pod-related API endpoints
func (s *Server) SetupPodEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("/api/pods", func(w http.ResponseWriter, r *http.Request) {
		response := s.highlanderService.GetPods(r.Context())

		w.Header().Set("Content-Type", "application/json")
		if response.Status == "error" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(response)
	})

	klog.Infof("Pod API endpoints configured")
}
