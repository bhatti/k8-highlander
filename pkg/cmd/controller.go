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
	"k8-highlander/pkg/common"
	"k8-highlander/pkg/leader"
	"k8-highlander/pkg/monitoring"
	"k8-highlander/pkg/server"
	"k8-highlander/pkg/storage"
	"k8-highlander/pkg/workloads"
	"k8-highlander/pkg/workloads/cronjob"
	"k8-highlander/pkg/workloads/persistent"
	"k8-highlander/pkg/workloads/process"
	"k8-highlander/pkg/workloads/service"
	"k8s.io/client-go/kubernetes"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/klog/v2"
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
	cluster, err := initializeCluster(&appConfig)
	if err != nil {
		return err
	}

	// Create a context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	workloadManager, err := initializeWorkloadManagers(ctx, cluster.GetClient(), &appConfig, metrics, monitoringServer)
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
	)

	// Handle graceful shutdown
	setupSignalHandler(ctx, cancel, leaderController, monitoringServer, httpServer)

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

// initializeCluster creates and configures a Kubernetes cluster client based on the
// provided application configuration.
//
// Parameters:
//   - cfg: Application configuration containing cluster settings
//
// Returns:
//   - *common.ClusterConfig: Configured cluster object with initialized Kubernetes client
//   - error: Error if client creation fails
func initializeCluster(cfg *common.AppConfig) (*common.ClusterConfig, error) {
	var cluster = cfg.Cluster

	// Initialize client for the cluster
	clientset, err := common.CreateKubernetesClient(cluster.Kubeconfig)
	if err != nil {
		return nil, err
	}

	cluster.SetClient(clientset)
	return &cluster, nil
}

// initializeWorkloadManagers creates and configures all workload managers for
// different types of workloads (processes, cron jobs, services, persistent sets).
// It registers each manager with the main workload manager.
//
// Parameters:
//   - ctx: Context for cancellation
//   - k8sClient: Kubernetes client interface
//   - cfg: Application configuration with workload definitions
//   - metrics: Metrics collector for the controller
//   - monitoringServer: Monitoring server for status reporting
//
// Returns:
//   - workloads.Manager: Configured workload manager with all workload types registered
//   - error: Error if any workload manager fails to initialize
func initializeWorkloadManagers(_ context.Context, _ kubernetes.Interface, cfg *common.AppConfig,
	metrics *monitoring.ControllerMetrics, monitoringServer *monitoring.MonitoringServer) (workloads.Manager, error) {
	// Create workload manager
	manager := workloads.NewManager(metrics, monitoringServer)

	// Add Process manager with processes from config
	processManager := process.NewProcessManager(cfg.Namespace, "", metrics, monitoringServer)
	for _, processConfig := range cfg.Workloads.Processes {
		if err := processManager.AddProcess(processConfig); err != nil {
			klog.Errorf("Failed to add process %s: %v", processConfig.Name, err)
		} else {
			klog.Infof("Added process %s", processConfig.Name)
		}
	}
	if err := manager.AddWorkload("processes", processManager); err != nil {
		return nil, err
	}

	// Add CronJob manager with cron jobs from config
	cronJobManager := cronjob.NewCronJobManager(cfg.Namespace, "", metrics, monitoringServer)
	for _, cronJobConfig := range cfg.Workloads.CronJobs {
		if err := cronJobManager.AddCronJob(cronJobConfig); err != nil {
			klog.Errorf("Failed to add cron job %s: %v", cronJobConfig.Name, err)
		} else {
			klog.Infof("Added cron job %s", cronJobConfig.Name)
		}
	}
	if err := manager.AddWorkload("cronjobs", cronJobManager); err != nil {
		return nil, err
	}

	// Add Deployment manager with deployments from config
	deploymentManager := service.NewDeploymentManager(cfg.Namespace, "", metrics, monitoringServer)
	for _, deploymentConfig := range cfg.Workloads.Services {
		if err := deploymentManager.AddDeployment(deploymentConfig); err != nil {
			klog.Errorf("Failed to add deployment %s: %v", deploymentConfig.Name, err)
		} else {
			klog.Infof("Added deployment %s", deploymentConfig.Name)
		}
	}
	if err := manager.AddWorkload("service", deploymentManager); err != nil {
		return nil, err
	}

	// Add StatefulSet manager with stateful sets from config
	statefulSetManager := persistent.NewPersistentManager(cfg.Namespace, "", metrics, monitoringServer)
	for _, statefulSetConfig := range cfg.Workloads.PersistentSets {
		if err := statefulSetManager.AddStatefulSet(statefulSetConfig); err != nil {
			klog.Errorf("Failed to add persistent set %s: %v", statefulSetConfig.Name, err)
		} else {
			klog.Infof("Added persistent set %s", statefulSetConfig.Name)
		}
	}
	if err := manager.AddWorkload("persistent", statefulSetManager); err != nil {
		return nil, err
	}

	return manager, nil
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
func setupSignalHandler(_ context.Context, cancel context.CancelFunc,
	leaderController *leader.LeaderController,
	monitoringServer *monitoring.MonitoringServer, httpServer *server.Server) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		klog.Infof("Received signal %s, initiating shutdown...", sig)

		// Stop leader controller first
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
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
