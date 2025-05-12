package workloads

import (
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"testing"
	"time"

	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	fake "k8s.io/client-go/kubernetes/fake"
)

// Helper function to create CRD objects for testing
func createCRDObject(kind, name, namespace, image string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"image": image,
				"script": map[string]interface{}{
					"commands": []interface{}{
						"echo 'Hello from CRD'",
						"sleep 60",
					},
					"shell": "/bin/sh",
				},
				"env": map[string]interface{}{
					"TEST_ENV": "crd-value",
				},
			},
		},
	}

	// Add kind-specific fields
	switch kind {
	case "WorkloadCronJob":
		obj.Object["spec"].(map[string]interface{})["schedule"] = "*/5 * * * *"
	case "WorkloadService", "WorkloadPersistent":
		obj.Object["spec"].(map[string]interface{})["replicas"] = int64(1)

		// Add ports for service and persistent
		obj.Object["spec"].(map[string]interface{})["ports"] = []interface{}{
			map[string]interface{}{
				"name":          "web",
				"containerPort": int64(80),
				"servicePort":   int64(8080),
			},
		}
	}

	return obj
}

// TestCRDWatcherCreation tests creating a new CRD watcher
func TestCRDWatcherCreation(t *testing.T) {
	// Create fake K8s clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create real manager and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)

	// Create a real manager
	manager, err := InitializeWorkloadManagers(context.Background(), mockK8sClient, &common.AppConfig{},
		metrics, monitoringServer)
	require.NoError(t, err)

	// Create the watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)

	// Verify watcher was created properly
	require.NotNil(t, watcher)
	require.Equal(t, mockDynamicClient, watcher.dynamicClient)
	require.Equal(t, mockK8sClient, watcher.k8sClient)
	require.Equal(t, manager, watcher.manager)
	require.Equal(t, metrics, watcher.metrics)
	require.Equal(t, monitoringServer, watcher.monitoringServer)
	require.NotNil(t, watcher.stopCh)
	require.NotNil(t, watcher.managedCRDs)
}

// TestFindWorkloadsByCRDRef tests finding workloads by CRD reference
func TestFindWorkloadsByCRDRef(t *testing.T) {
	// Create fake K8s clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create real metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)

	// Create a real manager
	manager, err := InitializeWorkloadManagers(context.Background(), mockK8sClient, &common.AppConfig{},
		metrics, monitoringServer)
	require.NoError(t, err)

	// Create the watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)

	// Create CRD references
	crdRef1 := &common.WorkloadCRDReference{
		APIVersion: common.GROUP + "/" + common.MajorVersion,
		Kind:       "WorkloadProcess",
		Name:       "test-process-1",
		Namespace:  "default",
	}

	crdRef2 := &common.WorkloadCRDReference{
		APIVersion: common.GROUP + "/" + common.MajorVersion,
		Kind:       "WorkloadProcess",
		Name:       "test-process-2",
		Namespace:  "default",
	}

	// Create mock workloads with CRD references
	mockWorkload1 := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:  "test-process-1",
			Image: "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{"echo test1"},
				Shell:    "/bin/sh",
			},
			WorkloadCRDRef: crdRef1,
		},
	}

	mockWorkload2 := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:  "test-process-2",
			Image: "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{"echo test2"},
				Shell:    "/bin/sh",
			},
			WorkloadCRDRef: crdRef2,
		},
	}

	// No CRD reference
	mockWorkload3 := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:  "test-process-3",
			Image: "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{"echo test3"},
				Shell:    "/bin/sh",
			},
		},
	}

	// Add workloads to the manager
	monitoringServer.UpdateLeaderStatus(true, "this-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)
	require.NoError(t, manager.GetWorkloadManager(common.WorkloadTypeProcessManager).AddWorkload(mockWorkload1))
	require.NoError(t, manager.GetWorkloadManager(common.WorkloadTypeProcessManager).AddWorkload(mockWorkload2))
	require.NoError(t, manager.GetWorkloadManager(common.WorkloadTypeProcessManager).AddWorkload(mockWorkload3))

	// Test finding workloads by CRD reference
	foundWorkloads := watcher.findWorkloadsByCRDRef(crdRef1)

	// Verify we found the right workload
	require.Len(t, foundWorkloads, 1)
	require.Equal(t, "test-process-1", foundWorkloads[0].GetName())
}

// TestWorkloadHasCRDRef tests checking if a workload has a CRD reference
func TestWorkloadHasCRDRef(t *testing.T) {
	// Create fake K8s clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create real metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)

	// Create a real manager
	manager, err := InitializeWorkloadManagers(context.Background(), mockK8sClient, &common.AppConfig{},
		metrics, monitoringServer)
	require.NoError(t, err)

	// Create the watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)

	// Create CRD references
	crdRef1 := &common.WorkloadCRDReference{
		APIVersion: common.GROUP + "/" + common.MajorVersion,
		Kind:       "WorkloadProcess",
		Name:       "test-process",
		Namespace:  "default",
	}

	crdRef2 := &common.WorkloadCRDReference{
		APIVersion: common.GROUP + "/" + common.MajorVersion,
		Kind:       "WorkloadProcess",
		Name:       "other-process",
		Namespace:  "default",
	}

	// Create a mock workload with a CRD reference
	mockWorkloadConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:  "test-process",
			Image: "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{"echo test"},
				Shell:    "/bin/sh",
			},
			WorkloadCRDRef: crdRef1,
		},
	}

	monitoringServer.UpdateLeaderStatus(true, "this-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)
	require.NoError(t, manager.GetWorkloadManager(common.WorkloadTypeProcessManager).AddWorkload(mockWorkloadConfig))

	mockWorkload, ok := manager.GetWorkloadManager(common.WorkloadTypeProcessManager).GetWorkload(mockWorkloadConfig.Name)
	require.True(t, ok)

	// Test with matching reference
	result := watcher.workloadHasCRDRef(mockWorkload, crdRef1)
	require.True(t, result)

	// Test with non-matching reference
	result = watcher.workloadHasCRDRef(mockWorkload, crdRef2)
	require.False(t, result)

	// Test with workload that has no CRD reference
	mockWorkloadNoCRDConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:  "test-process-no-crd",
			Image: "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{"echo test1"},
				Shell:    "/bin/sh",
			},
			WorkloadCRDRef: nil,
		},
	}
	require.NoError(t, manager.GetWorkloadManager(common.WorkloadTypeProcessManager).AddWorkload(mockWorkloadNoCRDConfig))
	mockWorkloadNoCRD, ok := manager.GetWorkloadManager(common.WorkloadTypeProcessManager).
		GetWorkload("test-process-no-crd")
	result = watcher.workloadHasCRDRef(mockWorkloadNoCRD, crdRef1)
	require.False(t, result)
}

// TestLeaderAwareness tests leader-aware behavior
func TestLeaderAwareness(t *testing.T) {
	// Create fake K8s clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create real metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)
	// Test with leader = false
	monitoringServer.UpdateLeaderStatus(false, "other-leader", "test-cluster")

	// Create a real manager
	manager, err := InitializeWorkloadManagers(context.Background(), mockK8sClient, &common.AppConfig{},
		metrics, monitoringServer)
	require.NoError(t, err)

	// Create the watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)

	// Check isLeaderActive()
	require.False(t, watcher.isLeaderActive(), "Should not be leader active")

	// Test with leader = true
	monitoringServer.UpdateLeaderStatus(true, "this-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)

	// Check isLeaderActive()
	require.True(t, watcher.isLeaderActive(), "Should be leader active")
}

// TestRemoveWorkloadWithCRDRef tests removing a workload by CRD reference
func TestRemoveWorkloadWithCRDRef(t *testing.T) {
	// Create a test context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create fake K8s clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create real metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)
	monitoringServer.UpdateLeaderStatus(true, "test-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)

	// Create a real manager
	manager, err := InitializeWorkloadManagers(context.Background(), mockK8sClient, &common.AppConfig{},
		metrics, monitoringServer)
	require.NoError(t, err)

	// Create a CRD reference
	crdRef := &common.WorkloadCRDReference{
		APIVersion: common.GROUP + "/" + common.MajorVersion,
		Kind:       "WorkloadProcess",
		Name:       "test-process",
		Namespace:  "default",
	}

	mockWorkloadConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:  "test-process",
			Image: "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{"echo test"},
				Shell:    "/bin/sh",
			},
			WorkloadCRDRef: crdRef,
		},
	}

	require.NoError(t, manager.StartAll(context.Background()))
	monitoringServer.UpdateLeaderStatus(true, "test-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)

	// Add workloads to the manager
	require.NoError(t, manager.GetWorkloadManager(common.WorkloadTypeProcessManager).AddWorkload(mockWorkloadConfig))

	// Verify workload exists before removal
	mockWorkload, ok := manager.GetWorkloadManager(common.WorkloadTypeProcessManager).GetWorkload(mockWorkloadConfig.Name)
	require.True(t, ok, "Workload should exist before removal")
	require.NotNil(t, mockWorkload, "Workload should not be nil before removal")

	// Create the watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)
	require.NoError(t, watcher.Start(context.Background()))

	// Test removing the workload
	err = watcher.removeWorkloadWithCRDRef(ctx, crdRef)
	require.NoError(t, err, "Should not error when removing workload")

	// Sleep a tiny bit to allow any async operations to complete
	time.Sleep(100 * time.Millisecond)

	// Verify workload is gone from the manager
	_, ok = manager.GetWorkloadManager(common.WorkloadTypeProcessManager).GetWorkload(mockWorkloadConfig.Name)
	require.False(t, ok, "Workload should not exist after removal")

	// Also verify it's gone from the top-level manager
	_, ok = manager.GetWorkload(mockWorkloadConfig.Name)
	require.False(t, ok, "Workload should not exist in top-level manager after removal")
}

// TestCRDEventHandlers tests the informer event handlers
func TestCRDEventHandlers(t *testing.T) {
	// Create fake K8s clients (ONLY the Kubernetes APIs should be mocked)
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create real metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)

	// Create a real manager
	manager, err := InitializeWorkloadManagers(context.Background(), mockK8sClient, &common.AppConfig{},
		metrics, monitoringServer)
	require.NoError(t, err)

	require.NoError(t, manager.StartAll(context.Background()))

	// Create the watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)

	// Setup fake dynamic client to return a mock CRD
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	processCRD := createCRDObject("WorkloadProcess", "test-process", "default", "test-image:latest")
	_, err = mockDynamicClient.Resource(gvr).Namespace("default").Create(context.Background(), processCRD, metav1.CreateOptions{})
	require.NoError(t, err)

	// Test OnCRDAdd
	watcher.OnCRDAdd(processCRD)

	// Test OnCRDUpdate
	updatedCRD := processCRD.DeepCopy()
	updatedCRD.Object["spec"].(map[string]interface{})["image"] = "updated-image:latest"
	updatedCRD.SetResourceVersion("2")
	watcher.OnCRDUpdate(processCRD, updatedCRD)

	// Test OnCRDDelete
	watcher.OnCRDDelete(processCRD)
}

// TestCRDEventHandlersWithTombstone tests the delete event handler with a tombstone
func TestCRDEventHandlersWithTombstone(t *testing.T) {
	// Create fake K8s clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create real metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)

	// Create a real manager
	manager, err := InitializeWorkloadManagers(context.Background(), mockK8sClient, &common.AppConfig{},
		metrics, monitoringServer)
	require.NoError(t, err)

	// Create the watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)

	// Create a test CRD object
	processCRD := createCRDObject("WorkloadProcess", "test-process", "default", "test-image:latest")

	// Create a DeletedFinalStateUnknown object
	tombstone := cache.DeletedFinalStateUnknown{
		Key: "default/test-process",
		Obj: processCRD,
	}

	// Test OnCRDDelete with tombstone - we can't verify internal state, but we can check for no panics
	watcher.OnCRDDelete(tombstone)
}

// TestCRDWatcherProcessChanges tests processing CRD changes (add, update, delete)
func TestCRDWatcherProcessChanges(t *testing.T) {
	// Create a test context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create fake K8s clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create real metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)
	monitoringServer.UpdateLeaderStatus(true, "test-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)

	// Create a real manager
	manager, err := InitializeWorkloadManagers(context.Background(), mockK8sClient, &common.AppConfig{},
		metrics, monitoringServer)
	require.NoError(t, err)

	// Create the watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)

	// Add a process config
	processCRD := createCRDObject("WorkloadProcess", "test-process", "default", "test-image:latest")

	// Test processing the CRD change (add) - we can't verify internal state, but we can check for no panics
	watcher.processCRDChange(ctx, processCRD, "add")

	// 2. Test Update - we can't verify internal state, but we can check for no panics
	// Update the process CRD
	updatedProcessCRD := processCRD.DeepCopy()
	updatedProcessCRD.Object["spec"].(map[string]interface{})["image"] = "updated-image:latest"

	// Test processing the CRD change (update)
	watcher.processCRDChange(ctx, updatedProcessCRD, "update")

	// 3. Test Delete - we can't verify internal state, but we can check for no panics
	// Test processing the CRD change (delete)
	watcher.processCRDChange(ctx, processCRD, "delete")
}

func TestCRDWatcherAddWorkload(t *testing.T) {
	// Create a test context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create fake clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)
	monitoringServer.UpdateLeaderStatus(true, "test-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)

	// Create manager
	manager, err := InitializeWorkloadManagers(ctx, mockK8sClient, &common.AppConfig{}, metrics, monitoringServer)
	require.NoError(t, err)

	// Create watcher with initially empty set of CRDs
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)
	require.NoError(t, watcher.Start(ctx))

	// Verify no workloads exist yet
	for _, mgr := range manager.GetWorkloadManagers() {
		require.Empty(t, mgr.GetWorkloadsWithCRD(), "Should have no CRD workloads initially")
	}

	// Create a CRD
	processCRD := createProcessCRD("test-process", "default")
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	// Add the CRD to the fake dynamic client
	_, err = mockDynamicClient.Resource(gvr).Namespace("default").Create(ctx, processCRD, metav1.CreateOptions{})
	require.NoError(t, err, "Should create CRD without error")

	// Manually trigger the OnCRDAdd event since we're using a fake client
	watcher.OnCRDAdd(processCRD)

	// Wait a bit for processing to complete
	time.Sleep(100 * time.Millisecond)

	// Check if workload was created
	processManager := manager.GetWorkloadManager(common.WorkloadTypeProcessManager)
	require.NotNil(t, processManager, "Process manager should not be nil")

	// Verify that workload was created
	workloads := processManager.GetWorkloadsWithCRD()
	require.Len(t, workloads, 1, "Should have created one workload")
	require.Equal(t, "test-process", workloads[0].GetName(), "Workload should have name from CRD")
}

func TestCRDWatcherUpdateWorkload(t *testing.T) {
	// Create a test context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create fake clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)
	monitoringServer.UpdateLeaderStatus(true, "test-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)

	// Create manager
	manager, err := InitializeWorkloadManagers(ctx, mockK8sClient, &common.AppConfig{}, metrics, monitoringServer)
	require.NoError(t, err)

	// Create watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)
	require.NoError(t, watcher.Start(ctx))

	// Create initial CRD
	processCRD := createProcessCRD("updatable-process", "default")
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	// Add the CRD to the fake dynamic client
	createdCRD, err := mockDynamicClient.Resource(gvr).Namespace("default").Create(ctx, processCRD, metav1.CreateOptions{})
	require.NoError(t, err)

	// Manually trigger the OnCRDAdd event
	watcher.OnCRDAdd(createdCRD)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Verify initial workload was created
	processManager := manager.GetWorkloadManager(common.WorkloadTypeProcessManager)
	workloads := processManager.GetWorkloadsWithCRD()
	require.Len(t, workloads, 1, "Should have created one workload")

	// Get the current workload config for comparison later
	initialWorkload, exists := processManager.GetWorkload("updatable-process")
	require.True(t, exists, "Initial workload should exist")
	initialConfig := initialWorkload.GetConfig()

	// Now update the CRD
	updatedCRD := updateProcessCRD(createdCRD)
	_, err = mockDynamicClient.Resource(gvr).Namespace("default").Update(ctx, updatedCRD, metav1.UpdateOptions{})
	require.NoError(t, err)

	// Manually trigger the OnCRDUpdate event
	watcher.OnCRDUpdate(createdCRD, updatedCRD)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Verify workload was updated
	updatedWorkload, exists := processManager.GetWorkload("updatable-process")
	require.True(t, exists, "Updated workload should exist")
	updatedConfig := updatedWorkload.GetConfig()

	// Verify config was updated
	require.NotEqual(t, initialConfig.Image, updatedConfig.Image, "Image should have been updated")
	require.Equal(t, "busybox:updated", updatedConfig.Image, "Image should match updated CRD value")
}

func TestCRDWatcherDeleteWorkload(t *testing.T) {
	// Create a test context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create fake clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)
	monitoringServer.UpdateLeaderStatus(true, "test-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)

	// Create manager
	manager, err := InitializeWorkloadManagers(ctx, mockK8sClient, &common.AppConfig{}, metrics, monitoringServer)
	require.NoError(t, err)

	// Create watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)

	require.NoError(t, watcher.Start(ctx))

	// Create initial CRD
	processCRD := createProcessCRD("deletable-process", "default")
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	// Add the CRD to the fake dynamic client
	createdCRD, err := mockDynamicClient.Resource(gvr).Namespace("default").Create(ctx, processCRD, metav1.CreateOptions{})
	require.NoError(t, err)

	// Manually trigger the OnCRDAdd event
	watcher.OnCRDAdd(createdCRD)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Verify workload was created
	processManager := manager.GetWorkloadManager(common.WorkloadTypeProcessManager)
	workloads := processManager.GetWorkloadsWithCRD()
	require.Len(t, workloads, 1, "Should have created one workload")

	// Now delete the CRD
	err = mockDynamicClient.Resource(gvr).Namespace("default").Delete(ctx, "deletable-process", metav1.DeleteOptions{})
	require.NoError(t, err)

	// Manually trigger the OnCRDDelete event
	watcher.OnCRDDelete(createdCRD)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Verify workload was removed
	workloads = processManager.GetWorkloadsWithCRD()
	require.Empty(t, workloads, "Should have removed the workload")

	_, exists := processManager.GetWorkload("deletable-process")
	require.False(t, exists, "Workload should not exist after deletion")
}

func TestCRDWatcherMultipleTypes(t *testing.T) {
	// Create a test context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create fake clients
	mockDynamicClient := dynamicfake.NewSimpleDynamicClient(common.CreateTestScheme())
	mockK8sClient := fake.NewClientset()

	// Create metrics and monitoring
	metrics := monitoring.NewControllerMetrics(prometheus.NewRegistry())
	monitoringServer := monitoring.NewMonitoringServer(metrics, common.VERSION, common.BuildInfo)
	monitoringServer.UpdateLeaderStatus(true, "test-leader", "test-cluster")
	monitoringServer.UpdateControllerState(common.StateNormal)

	// Create manager
	manager, err := InitializeWorkloadManagers(ctx, mockK8sClient, &common.AppConfig{}, metrics, monitoringServer)
	require.NoError(t, err)

	// Create watcher
	watcher := NewCRDWatcher(mockDynamicClient, mockK8sClient, manager, metrics, monitoringServer)
	require.NoError(t, watcher.Start(ctx))

	// Create CRDs of different types
	processCRD := createProcessCRD("multi-process", "default")
	cronJobCRD := createCronJobCRD("multi-cronjob", "default")
	serviceCRD := createServiceCRD("multi-service", "default")

	// Define GVRs for each type
	processGVR := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	cronJobGVR := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.CronjobsResource,
	}

	serviceGVR := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ServicesResource,
	}

	// Add the CRDs to the fake dynamic client
	createdProcessCRD, err := mockDynamicClient.Resource(processGVR).Namespace("default").Create(ctx, processCRD, metav1.CreateOptions{})
	require.NoError(t, err)

	createdCronJobCRD, err := mockDynamicClient.Resource(cronJobGVR).Namespace("default").Create(ctx, cronJobCRD, metav1.CreateOptions{})
	require.NoError(t, err)

	createdServiceCRD, err := mockDynamicClient.Resource(serviceGVR).Namespace("default").Create(ctx, serviceCRD, metav1.CreateOptions{})
	require.NoError(t, err)

	// Manually trigger the OnCRDAdd events
	watcher.OnCRDAdd(createdProcessCRD)
	watcher.OnCRDAdd(createdCronJobCRD)
	watcher.OnCRDAdd(createdServiceCRD)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Verify workloads of different types were created
	processManager := manager.GetWorkloadManager(common.WorkloadTypeProcessManager)
	cronJobManager := manager.GetWorkloadManager(common.WorkloadTypeCronJobManager)
	serviceManager := manager.GetWorkloadManager(common.WorkloadTypeServiceManager)

	require.Len(t, processManager.GetWorkloadsWithCRD(), 1, "Should have created one process workload")
	require.Len(t, cronJobManager.GetWorkloadsWithCRD(), 1, "Should have created one cronjob workload")
	require.Len(t, serviceManager.GetWorkloadsWithCRD(), 1, "Should have created one service workload")

	// Verify the workloads have the correct names
	processWorkloads := processManager.GetWorkloadsWithCRD()
	cronJobWorkloads := cronJobManager.GetWorkloadsWithCRD()
	serviceWorkloads := serviceManager.GetWorkloadsWithCRD()

	require.Equal(t, "multi-process", processWorkloads[0].GetName())
	require.Equal(t, "multi-cronjob", cronJobWorkloads[0].GetName())
	require.Equal(t, "multi-service", serviceWorkloads[0].GetName())
}

// Helper function to create a process CRD
func createProcessCRD(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadProcess",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"image": "busybox:crd-version",
				"script": map[string]interface{}{
					"commands": []interface{}{
						"echo 'Starting CRD-defined process'",
						"echo 'Process ID: $$'",
						"sleep 300", // Sleep for 5 minutes
					},
					"shell": "/bin/sh",
				},
				"env": map[string]interface{}{
					"CRD_CONFIG": "from-crd",
				},
				"resources": map[string]interface{}{
					"cpuRequest":    "100m",
					"memoryRequest": "64Mi",
					"cpuLimit":      "200m",
					"memoryLimit":   "128Mi",
				},
				"restartPolicy": "Never",
				"maxRestarts":   int64(3),
				"gracePeriod":   "30s",
			},
		},
	}
}

// Helper function to update a process CRD
func updateProcessCRD(original *unstructured.Unstructured) *unstructured.Unstructured {
	updated := original.DeepCopy()

	// Update the image
	if spec, ok := updated.Object["spec"].(map[string]interface{}); ok {
		spec["image"] = "busybox:updated"

		// Update env
		if env, ok := spec["env"].(map[string]interface{}); ok {
			env["CRD_CONFIG"] = "updated-from-crd"
		}

		// Update script commands
		if script, ok := spec["script"].(map[string]interface{}); ok {
			script["commands"] = []interface{}{
				"echo 'Starting UPDATED CRD-defined process'",
				"echo 'Process ID: $$'",
				"sleep 300",
			}
		}
	}

	return updated
}

// Helper function to create a cron job CRD
func createCronJobCRD(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadCronJob",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"image": "busybox:crd-version",
				"script": map[string]interface{}{
					"commands": []interface{}{
						"echo 'Running CRD-defined cron job'",
						"echo 'Running at: $(date)'",
						"sleep 10",
					},
					"shell": "/bin/sh",
				},
				"schedule": "*/5 * * * *", // Every 5 minutes
				"env": map[string]interface{}{
					"CRD_CONFIG": "from-crd",
				},
				"resources": map[string]interface{}{
					"cpuRequest":    "50m",
					"memoryRequest": "32Mi",
					"cpuLimit":      "100m",
					"memoryLimit":   "64Mi",
				},
				"restartPolicy": "OnFailure",
			},
		},
	}
}

// Helper function to create a service CRD
func createServiceCRD(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadService",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"image": "nginx:alpine",
				"script": map[string]interface{}{
					"commands": []interface{}{
						"nginx -g 'daemon off;'",
					},
					"shell": "/bin/sh",
				},
				"replicas": int64(2), // CRD specifies 2 replicas
				"ports": []interface{}{
					map[string]interface{}{
						"name":          "http",
						"containerPort": int64(80),
						"servicePort":   int64(8080),
					},
				},
				"env": map[string]interface{}{
					"CRD_CONFIG": "from-crd",
				},
				"resources": map[string]interface{}{
					"cpuRequest":    "100m",
					"memoryRequest": "64Mi",
					"cpuLimit":      "200m",
					"memoryLimit":   "128Mi",
				},
				"healthCheckPath": "/health",
				"healthCheckPort": int64(80),
			},
		},
	}
}
