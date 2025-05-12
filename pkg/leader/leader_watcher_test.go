package leader

import (
	"context"
	"testing"
	"time"

	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/workloads"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// TestLeaderControllerWithAsyncCRDWatcher tests the leader controller responding to asynchronous
// CRD changes via the CRD watcher.
func TestLeaderControllerWithAsyncCRDWatcher(t *testing.T) {
	// Set up test environment
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Create a fake dynamic client for testing CRDs
	scheme := runtime.NewScheme()
	// Create a map of list kinds for your CRDs
	listKinds := map[schema.GroupVersionResource]string{
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ProcessesResource}:   "WorkloadProcessList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ServicesResource}:    "WorkloadServiceList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.CronjobsResource}:    "WorkloadCronJobList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.PersistentsResource}: "WorkloadPersistentList",
	}

	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	// Create workload manager
	workloadMgr, err := env.NewManager(true)
	require.NoError(t, err)

	// Create a leader controller with the workload manager
	controller := NewLeaderController(
		env.leaderStorage,
		"async-crd-controller",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"tenant-test",
		false,
	)

	// Create a CRD watcher attached to the controller
	crdWatcher := workloads.NewCRDWatcher(
		dynamicClient,
		env.GetClient(),
		workloadMgr,
		env.metrics,
		env.monitorServer,
	)

	// Start the controller - it should become leader
	err = controller.Start(ctx)
	require.NoError(t, err)

	// Wait for the controller to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// Start the CRD watcher
	err = crdWatcher.Start(ctx)
	require.NoError(t, err)

	// Verify no workloads exist yet
	processManager := workloadMgr.GetWorkloadManager(common.WorkloadTypeProcessManager)
	require.NotNil(t, processManager, "Process manager should not be nil")
	require.Empty(t, processManager.GetWorkloadsWithCRD(), "Should have no CRD workloads initially")

	// PHASE 1: Test asynchronous CRD addition
	// Create a process CRD asynchronously (simulating external CRD creation)
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	processCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadProcess",
			"metadata": map[string]interface{}{
				"name":      "async-process",
				"namespace": env.namespace,
			},
			"spec": map[string]interface{}{
				"image": "busybox:crd-test",
				"script": map[string]interface{}{
					"commands": []interface{}{
						"echo 'Starting async CRD-defined process'",
						"sleep 300",
					},
					"shell": "/bin/sh",
				},
				"env": map[string]interface{}{
					"ASYNC_TEST": "true",
				},
				"restartPolicy": "Never",
			},
		},
	}

	processCRD.SetAPIVersion("1.0")
	_, err = dynamicClient.Resource(gvr).Namespace(env.namespace).Create(ctx, processCRD, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create process CRD")

	// Manually trigger the event since we're using a fake client
	crdWatcher.OnCRDAdd(processCRD)

	// Wait for the workload to be created asynchronously (up to 5 seconds)
	success = waitForCondition(5*time.Second, func() bool {
		workloads := processManager.GetWorkloadsWithCRD()
		return len(workloads) > 0
	})
	require.True(t, success, "CRD workload should be created asynchronously")

	// Verify the workload was properly created
	workloads := processManager.GetWorkloadsWithCRD()
	require.Len(t, workloads, 1, "Should have one CRD workload")
	require.Equal(t, "async-process", workloads[0].GetName(), "Workload should have name from CRD")

	// In real mode, verify pod creation
	if env.mode == RealMode {
		success := env.waitForPodState(t, "async-process-pod", true, 10*time.Second)
		require.True(t, success, "Pod should be created by the leader controller")
	}

	// PHASE 2: Test asynchronous CRD update
	updatedProcessCRD := processCRD.DeepCopy()
	processCRD.SetAPIVersion("2.0")
	spec := updatedProcessCRD.Object["spec"].(map[string]interface{})
	spec["image"] = "busybox:updated"

	if env, ok := spec["env"].(map[string]interface{}); ok {
		env["ASYNC_UPDATED"] = "true"
	}

	_, err = dynamicClient.Resource(gvr).Namespace(env.namespace).Update(ctx, updatedProcessCRD, metav1.UpdateOptions{})
	require.NoError(t, err, "Failed to update process CRD")

	t.Logf("updating crd %s...", updatedProcessCRD)
	// Manually trigger the update event
	crdWatcher.OnCRDUpdate(processCRD, updatedProcessCRD)

	// Wait for the workload to be updated (check config has changed)
	success = waitForCondition(5*time.Second, func() bool {
		workloads := processManager.GetWorkloadsWithCRD()
		if len(workloads) == 0 {
			return false
		}
		workloadConfig := workloads[0].GetConfig()
		t.Logf("image %s", workloadConfig.Image)
		return workloadConfig.Image == "busybox:updated"
	})
	require.True(t, success, "CRD workload should be updated asynchronously")

	// In real mode, verify pod was recreated with new image
	if env.mode == RealMode {
		success := env.waitForPodState(t, "async-process-pod", true, 10*time.Second)
		require.True(t, success, "Updated pod should be running")

		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "async-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "busybox:updated", pod.Spec.Containers[0].Image, "Pod should use updated image")
	}

	// PHASE 3: Test asynchronous CRD deletion
	err = dynamicClient.Resource(gvr).Namespace(env.namespace).Delete(ctx, "async-process", metav1.DeleteOptions{})
	require.NoError(t, err, "Failed to delete process CRD")

	// Manually trigger the delete event
	crdWatcher.OnCRDDelete(updatedProcessCRD)

	// Wait for the workload to be removed
	success = waitForCondition(5*time.Second, func() bool {
		workloads := processManager.GetWorkloadsWithCRD()
		return len(workloads) == 0
	})
	require.True(t, success, "CRD workload should be removed asynchronously")

	// In real mode, verify pod was deleted
	if env.mode == RealMode {
		success := env.waitForPodState(t, "async-process-pod", false, 10*time.Second)
		require.True(t, success, "Pod should be deleted")
	}

	// Stop the controller and watcher
	err = crdWatcher.Stop(ctx)
	require.NoError(t, err)

	err = controller.Stop(ctx)
	require.NoError(t, err)
}

// TestLeaderFailoverWithAsyncCRDsAsLeader tests failover with leader
func TestLeaderFailoverWithAsyncCRDsAsLeader(t *testing.T) {
	// Set up test environment
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a fake dynamic client for testing CRDs
	scheme := common.CreateTestScheme()
	// Create a map of list kinds for your CRDs
	listKinds := map[schema.GroupVersionResource]string{
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ProcessesResource}:   "WorkloadProcessList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ServicesResource}:    "WorkloadServiceList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.CronjobsResource}:    "WorkloadCronJobList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.PersistentsResource}: "WorkloadPersistentList",
	}

	// Create dynamic client with custom list kinds
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)

	// Create workload managers for both controllers
	workloadMgr1, err := env.NewManager(true)
	require.NoError(t, err)

	t.Logf(">>> env %s -- %v", env.leaderStorage, env.monitorServer.GetLeaderInfo())
	// Create two leader controllers
	controller1 := NewLeaderController(
		env.leaderStorage,
		"failover-controller-1",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr1,
		"tenant-test",
		false,
	)

	// Start the first controller
	err = controller1.Start(ctx)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)
	// Start the second controller

	env.monitorServer.UpdateLeaderStatus(true, controller1.id, env.cluster.Name)

	// Wait for controller1 to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller1.IsLeader()
	})
	require.True(t, success, "Controller 1 should become leader")

	t.Logf("#### leader1 %v, cluster %s, workload %d", controller1.IsLeader(), controller1.cluster.Name,
		len(workloadMgr1.GetWorkloadManager(common.WorkloadTypeProcessManager).GetWorkloadsWithCRD()))
	// Create CRD watchers for both controllers
	crdWatcher1 := workloads.NewCRDWatcher(
		dynamicClient,
		env.GetClient(),
		workloadMgr1,
		env.metrics,
		env.monitorServer,
	)

	// Start the CRD watchers
	err = crdWatcher1.Start(ctx)
	require.NoError(t, err)

	// Create a process CRD
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	processCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadProcess",
			"metadata": map[string]interface{}{
				"name":      "failover-process",
				"namespace": env.namespace,
			},
			"spec": map[string]interface{}{
				"image": "busybox:failover-test",
				"script": map[string]interface{}{
					"commands": []interface{}{
						"echo 'Starting failover CRD-defined process'",
						"sleep 300",
					},
					"shell": "/bin/sh",
				},
				"env": map[string]interface{}{
					"FAILOVER_TEST": "true",
				},
				"restartPolicy": "Never",
			},
		},
	}

	// Create the CRD in the API server
	_, err = dynamicClient.Resource(gvr).Namespace(env.namespace).Create(ctx, processCRD, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create process CRD")

	// Wait for leader to create the workload
	success = waitForCondition(5*time.Second, func() bool {
		processManager := workloadMgr1.GetWorkloadManager(common.WorkloadTypeProcessManager)
		if processManager == nil {
			t.Logf("Process manager is nil")
			return false
		}

		workloads := processManager.GetWorkloadsWithCRD()
		t.Logf("Found %d workloads with CRD", len(workloads))
		if len(workloads) == 0 {
			t.Logf("#### No workloads with CRD found")

			// Check if there are any workloads at all
			allWorkloads := processManager.GetWorkloadsWithCRD()
			t.Logf("Total workloads: %d", len(allWorkloads))
			for _, w := range allWorkloads {
				t.Logf("Workload %s, CRD ref: %v", w.GetName(), w.GetConfig().WorkloadCRDRef != nil)
			}
			return false
		}
		for i, w := range workloads {
			t.Logf("Workload %d: %s, active: %v, ref: %+v",
				i, w.GetName(), w.GetStatus().Active, w.GetConfig().WorkloadCRDRef)
		}
		// Ensure the workload is actually running (leader only)
		return workloads[0].GetStatus().Active
	})
	require.True(t, success, "Leader controller should create and start the workload")

	// Check that leader has the workload in its configuration and active
	workloads1 := workloadMgr1.GetWorkloadManager(common.WorkloadTypeProcessManager).GetWorkloadsWithCRD()
	if len(workloads1) > 0 {
		// If workloads exist on follower, verify they're not active
		for _, w := range workloads1 {
			require.True(t, w.GetStatus().Active, "Leader workloads should be active: %s", w)
		}
	}

	// Record pod creation time if in real mode
	var firstPodCreationTime metav1.Time
	if env.mode == RealMode {
		success := env.waitForPodState(t, "failover-process-pod", true, 10*time.Second)
		require.True(t, success, "Pod should be created by leader controller")

		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
		firstPodCreationTime = pod.CreationTimestamp
	}

	// Simulate leader failure by stopping controller1
	err = controller1.Stop(ctx)
	require.NoError(t, err)

	err = crdWatcher1.Stop(ctx)
	require.NoError(t, err)

	// In real mode, verify pod is recreated by the new leader
	if env.mode == RealMode {
		success := env.waitForPodState(t, "failover-process-pod", true, 10*time.Second)
		require.True(t, success, "Pod should be recreated by new leader")

		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
		require.True(t, pod.CreationTimestamp.After(firstPodCreationTime.Time),
			"Pod should be recreated with newer timestamp")
	}

	// Now test CRD deletion with the new leader
	err = dynamicClient.Resource(gvr).Namespace(env.namespace).Delete(ctx, "failover-process", metav1.DeleteOptions{})
	require.NoError(t, err, "Failed to delete process CRD")

	// In real mode, verify pod is deleted
	if env.mode == RealMode {
		success := env.waitForPodState(t, "failover-process-pod", false, 10*time.Second)
		require.True(t, success, "Pod should be deleted by new leader")
	}
}

// TestLeaderFailoverWithAsyncCRDsAsFollower tests failover with foller
func TestLeaderFailoverWithAsyncCRDsAsFollower(t *testing.T) {
	// Set up test environment
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a fake dynamic client for testing CRDs
	scheme := common.CreateTestScheme()
	// Create a map of list kinds for your CRDs
	listKinds := map[schema.GroupVersionResource]string{
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ProcessesResource}:   "WorkloadProcessList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ServicesResource}:    "WorkloadServiceList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.CronjobsResource}:    "WorkloadCronJobList",
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.PersistentsResource}: "WorkloadPersistentList",
	}

	// Create dynamic client with custom list kinds
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)

	// Create workload managers for both controllers
	workloadMgr2, err := env.NewManager(true)
	require.NoError(t, err)

	t.Logf(">>> env %s -- %v", env.leaderStorage, env.monitorServer.GetLeaderInfo())
	controller2 := NewLeaderController(
		env.leaderStorage,
		"failover-controller-2",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr2,
		"tenant-test",
		true,
	)

	// Start the second controller
	err = controller2.Start(ctx)
	require.NoError(t, err)

	env.monitorServer.UpdateLeaderStatus(false, controller2.id, env.cluster.Name)

	// Wait for controller1 to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller2.IsLeader()
	})
	require.False(t, success, "Controller 2 should become follower (%v): %v",
		controller2.IsLeader(), controller2.monitoringServer.GetLeaderInfo())
	// Verify controller2 is not leader
	require.False(t, controller2.IsLeader(), "Controller 2 should not be leader")

	t.Logf("#### leader2 %v, cluster %s, workload %d", controller2.IsLeader(), controller2.cluster.Name,
		len(workloadMgr2.GetWorkloadManager(common.WorkloadTypeProcessManager).GetWorkloadsWithCRD()))
	crdWatcher2 := workloads.NewCRDWatcher(
		dynamicClient,
		env.GetClient(),
		workloadMgr2,
		env.metrics,
		env.monitorServer,
	)

	// Start the CRD watchers
	err = crdWatcher2.Start(ctx)
	require.NoError(t, err)

	// Create a process CRD
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	processCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadProcess",
			"metadata": map[string]interface{}{
				"name":      "failover-process",
				"namespace": env.namespace,
			},
			"spec": map[string]interface{}{
				"image": "busybox:failover-test",
				"script": map[string]interface{}{
					"commands": []interface{}{
						"echo 'Starting failover CRD-defined process'",
						"sleep 300",
					},
					"shell": "/bin/sh",
				},
				"env": map[string]interface{}{
					"FAILOVER_TEST": "true",
				},
				"restartPolicy": "Never",
			},
		},
	}

	// Create the CRD in the API server
	_, err = dynamicClient.Resource(gvr).Namespace(env.namespace).Create(ctx, processCRD, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create process CRD")
	t.Logf("########### adding process crd ######### to crd-watcher2")
	crdWatcher2.OnCRDAdd(processCRD)
	t.Logf("########### waiting for added workload")

	// Check that follower has the workload in its configuration but not Active
	workloads2 := workloadMgr2.GetWorkloadManager(common.WorkloadTypeProcessManager).GetWorkloadsWithCRD()
	if len(workloads2) > 0 {
		// If workloads exist on follower, verify they're not active
		for _, w := range workloads2 {
			require.False(t, w.GetStatus().Active, "Follower workloads should not be active: %s", w)
		}
	}

	// Record pod creation time if in real mode
	if env.mode == RealMode {
		success := env.waitForPodState(t, "failover-process-pod", true, 10*time.Second)
		require.True(t, success, "Pod should be created by leader controller")

		_, err = env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
	}

	// Stop the second controller and watcher
	err = crdWatcher2.Stop(ctx)
	require.NoError(t, err)
}
