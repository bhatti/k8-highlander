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

// Package cmd implements the core controller functionality for k8-highlander.
//
// K8 Highlander allows managing singleton or stateful processes in Kubernetes
// clusters where only one instance of a service should exist at any time. This
// package initializes and orchestrates all major components including:
//
// - Leader election to ensure only one controller is active at a time
// - Workload management for processes, cron jobs, services, and stateful sets
// - Monitoring with Prometheus metrics
// - HTTP API server for status and configuration
// - Graceful shutdown handling
//
// Usage Warning:
//
// The controller uses leader election to ensure only one instance
// manages workloads at a time. Multiple controller instances can be deployed
// for high availability, but ensure they share the same storage backend
// (Redis or database) to prevent split-brain issues.
//
// Proper RBAC permissions are required for the controller to manage Kubernetes
// resources. The controller needs permissions to create, update, and delete
// pods, deployments, statefulsets, and other related resources.
//
// In production environments, consider resource limits, monitoring, and proper
// storage configuration to ensure controller reliability.

package cmd

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/leader"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"github.com/bhatti/k8-highlander/pkg/server"
	"github.com/bhatti/k8-highlander/pkg/storage"
	"github.com/bhatti/k8-highlander/pkg/workloads"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/klog/v2"
	"os"
	"os/signal"
	"syscall"
)

// runController initializes and starts all components of the k8-highlander controller.
// It handles the orchestration of:
// - Kubernetes cluster configuration
// - Monitoring and metrics
// - Workload management for all supported workload types
// - HTTP API server
// - Leader election
// - Graceful shutdown
//
// This is the main entry point for the controller execution flow.
//
// Returns an error if any component fails to initialize or start.
func runController() error {
	// Initialize clusters
	cluster := &appConfig.Cluster

	// Create a context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create apiextensions clientset for CRD management
	apiextensionsClient, err := apiextensionsclientset.NewForConfig(cluster.SelectedKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	// Create and initialize CRD registry
	crdRegistry := common.NewCRDRegistry(apiextensionsClient)
	if err := crdRegistry.RegisterCRDs(ctx); err != nil {
		klog.Warningf("Failed to register CRDs: %v. Continuing anyway as they may already exist.", err)
	}

	// Initialize monitoring
	metrics := monitoring.NewControllerMetrics(nil) // Use default registry
	monitoringServer := monitoring.NewMonitoringServer(
		metrics,
		common.VERSION,
		common.BuildInfo,
	)
	if err := monitoringServer.Start(ctx); err != nil {
		return err
	}

	// Initialize workload manager
	workloadManager, err := workloads.InitializeWorkloadManagers(ctx, cluster.GetClient(), &appConfig, metrics, monitoringServer)
	if err != nil {
		return err
	}

	// Initialize HTTP server
	httpServer := server.NewServer(&appConfig, monitoringServer)
	if err := httpServer.Start(ctx, workloadManager, cluster.GetClient(), appConfig.Namespace); err != nil {
		return err
	}

	// Initialize leader election
	storageConfig, err := appConfig.BuildStorageConfig()
	if err != nil {
		return err
	}
	leaderStorage, err := storage.NewLeaderStorage(storageConfig)
	if err != nil {
		return err
	}

	leaderController := leader.NewLeaderController(
		leaderStorage,
		appConfig.ID,
		cluster,
		metrics,
		monitoringServer,
		workloadManager,
		appConfig.Tenant,
		appConfig.RemainFollower,
	)

	// Handle graceful shutdown
	setupSignalHandler(ctx, cancel, &appConfig, leaderController, monitoringServer, httpServer)

	// Start the leader election process
	if err := leaderController.Start(ctx); err != nil {
		return err
	}

	klog.Infof("k8-highlander started with ID %s, tenant %s [%v]",
		appConfig.ID, appConfig.Tenant, monitoringServer.GetLeaderInfo())

	// Block until context is canceled
	<-ctx.Done()
	klog.Info("Main context canceled, exiting")
	return nil
}

// setupSignalHandler configures signal handling for graceful shutdown of all components.
// It catches SIGINT and SIGTERM signals and performs an orderly shutdown sequence.
//
// Parameters:
//   - ctx: Main context (not directly used but maintained for future flexibility)
//   - cancel: Function to cancel the main context
//   - leaderController: Leader controller to stop
//   - monitoringServer: Monitoring server to stop
//   - httpServer: HTTP server to stop
//
// The shutdown sequence:
// 1. Stop the leader controller (releasing leadership if held)
// 2. Stop the monitoring server
// 3. Stop the HTTP server
// 4. Cancel the main context to signal completion
func setupSignalHandler(_ context.Context, cancel context.CancelFunc, cfg *common.AppConfig,
	leaderController *leader.LeaderController,
	monitoringServer *monitoring.MonitoringServer, httpServer *server.Server) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		klog.Infof("Received signal %s, initiating shutdown...", sig)

		// Stop leader controller first
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.TerminationGracePeriod)
		defer shutdownCancel()

		klog.Infof("Stopping leader controller (isLeader: %v)...", leaderController.IsLeader())
		if err := leaderController.Stop(shutdownCtx); err != nil {
			klog.Errorf("Error stopping leader controller: %v", err)
		}

		// Stop monitoring server
		klog.Infof("Stopping monitoring server...")
		if err := monitoringServer.Stop(shutdownCtx); err != nil {
			klog.Errorf("Error stopping monitoring server: %v", err)
		}

		// Stop HTTP server
		klog.Infof("Stopping HTTP server...")
		if err := httpServer.Stop(shutdownCtx); err != nil {
			klog.Errorf("Error stopping HTTP server: %v", err)
		}

		// Cancel the main context
		cancel()
		klog.Infof("Shutdown sequence completed")
	}()
}
