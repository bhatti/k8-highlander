package leader

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/workloads/process"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLeaderWithCRDWorkload tests the leader controller with a workload defined in CRD
func TestLeaderWithCRDWorkload(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Create a fake dynamic client for testing CRDs
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme)

	// Create a temporary directory for config files
	tempDir, err := os.MkdirTemp("", "crd-test-configs")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create the CRD definition
	createTestProcessCRD(t, dynamicClient, env.namespace)

	// Create a process workload with CRD reference
	processConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:      "process-from-crd",
			Namespace: env.namespace,
			// Minimal configuration since we'll get most from CRD
			Image: "busybox:latest", // Fallback image
			Script: &common.ScriptConfig{
				Commands: []string{"echo 'Fallback script'"},
				Shell:    "/bin/sh",
			},
			WorkloadCRDRef: &common.WorkloadCRDReference{
				APIVersion: common.GROUP + "/" + common.MajorVersion,
				Kind:       "WorkloadProcess",
				Name:       "test-process-crd",
				Namespace:  env.namespace,
			},
		},
	}

	// Create workload manager
	workloadMgr, err := env.NewManager(true)
	require.NoError(t, err)

	// Create process workload and add to manager
	processWorkload, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer, env.cluster.GetClient())
	require.NoError(t, err)
	err = workloadMgr.AddWorkload(processWorkload)
	require.NoError(t, err)

	// Create a controller with our workload manager
	controller := NewLeaderController(
		env.leaderStorage,
		"crd-test-controller",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"tenant",
		false,
	)

	// TODO Set up the LoadWorkloadFromCRD function to use our test dynamic client

	// Start the controller
	err = controller.Start(ctx)
	require.NoError(t, err)

	// Wait for leadership stabilization
	time.Sleep(2 * time.Second)

	// Verify controller became leader
	require.True(t, controller.IsLeader(), "Controller should become leader")

	// In real mode, verify the pod is created with CRD settings
	if env.mode == RealMode {
		success := env.waitForPodState(t, "process-from-crd-pod", true, 15*time.Second)
		require.True(t, success, "Process pod from CRD should be running")

		// Get the pod to verify CRD settings were applied
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "process-from-crd-pod", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "busybox:crd-version", pod.Spec.Containers[0].Image, "Pod should use image from CRD")

		// Check environment variable from CRD
		foundCRDEnv := false
		for _, env := range pod.Spec.Containers[0].Env {
			if env.Name == "CRD_CONFIG" && env.Value == "from-crd" {
				foundCRDEnv = true
				break
			}
		}
		require.True(t, foundCRDEnv, "CRD environment variable should be present")
	}

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)

	// In real mode, verify the pod is deleted
	if env.mode == RealMode {
		success := env.waitForPodState(t, "process-from-crd-pod", false, 15*time.Second)
		require.True(t, success, "Process pod should be deleted")
	}
}

// TestLeaderCRDFailover tests leader failover with workloads defined in CRDs
func TestLeaderCRDFailover(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Create a fake dynamic client for testing CRDs
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme)

	// Create CRD resources
	createTestProcessCRD(t, dynamicClient, env.namespace)

	// Define process configuration with CRD reference for both controllers
	processConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:      "failover-process",
			Namespace: env.namespace,
			// Minimal configuration since we'll get most from CRD
			Image: "busybox:latest", // Fallback image
			Script: &common.ScriptConfig{
				Commands: []string{"echo 'Fallback script'"},
				Shell:    "/bin/sh",
			},
			WorkloadCRDRef: &common.WorkloadCRDReference{
				APIVersion: common.GROUP + "/" + common.MajorVersion,
				Kind:       "WorkloadProcess",
				Name:       "test-process-crd",
				Namespace:  env.namespace,
			},
		},
	}

	// Create workload managers for both controllers
	workloadMgr1, err := env.NewManager(true)
	workloadMgr2, err := env.NewManager(true)

	// Create process workload for controller 1
	processWorkload1, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer, env.cluster.GetClient())
	require.NoError(t, err)
	err = workloadMgr1.AddWorkload(processWorkload1)
	require.NoError(t, err)

	// Create process workload for controller 2
	processWorkload2, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer, env.cluster.GetClient())
	require.NoError(t, err)
	err = workloadMgr2.AddWorkload(processWorkload2)
	require.NoError(t, err)

	// Create controllers
	controller1 := NewLeaderController(
		env.leaderStorage,
		"crd-failover-controller-1",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr1,
		"tenant",
		false,
	)

	controller2 := NewLeaderController(
		env.leaderStorage,
		"crd-failover-controller-2",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr2,
		"tenant",
		false,
	)

	// TODO Set up the LoadWorkloadFromCRD function to use our test dynamic client

	// Start both controllers
	err = controller1.Start(ctx)
	require.NoError(t, err)

	err = controller2.Start(ctx)
	require.NoError(t, err)

	// Wait for controller1 to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller1.IsLeader()
	})
	require.True(t, success, "Controller 1 should become leader")
	require.False(t, controller2.IsLeader(), "Controller 2 should not be leader")

	// In real mode, check if pod was created by controller1
	var firstPodCreationTime metav1.Time
	if env.mode == RealMode {
		success := env.waitForPodState(t, "failover-process-pod", true, 15*time.Second)
		require.True(t, success, "Process pod should be running")

		// Get pod to record creation timestamp
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
		firstPodCreationTime = pod.CreationTimestamp
		t.Logf("First pod created at: %v by controller 1", firstPodCreationTime)
	}

	// In real mode, check if pod was created by controller1
	if env.mode == RealMode {
		success := env.waitForPodState(t, "failover-process-pod", true, 15*time.Second)
		require.True(t, success, "Process pod should be running")

		// Get pod to record creation timestamp
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
		firstPodCreationTime = pod.CreationTimestamp
		t.Logf("First pod created at: %v by controller 1", firstPodCreationTime)
	}

	// Stop controller 1 to simulate failure
	t.Log("Stopping controller 1 to simulate failure")
	err = controller1.Stop(ctx)
	require.NoError(t, err)

	// Wait for controller 2 to become leader
	success = waitForCondition(15*time.Second, func() bool {
		return controller2.IsLeader()
	})
	require.True(t, success, "Controller 2 should become leader after controller 1 failure")

	// In real mode, check if pod was recreated by controller2
	if env.mode == RealMode {
		// Wait for the pod to be recreated
		success := env.waitForPodState(t, "failover-process-pod", true, 15*time.Second)
		require.True(t, success, "Process pod should be recreated and running")

		// Get pod to check creation timestamp
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
		secondPodCreationTime := pod.CreationTimestamp
		t.Logf("Second pod created at: %v by controller 2", secondPodCreationTime)

		// Verify the pod was recreated after failover
		require.True(t, secondPodCreationTime.After(firstPodCreationTime.Time),
			"New pod should be created after the original pod")
	}

	// Stop controller 2
	err = controller2.Stop(ctx)
	require.NoError(t, err)

	// In real mode, verify pod was deleted
	if env.mode == RealMode {
		success := env.waitForPodState(t, "failover-process-pod", false, 15*time.Second)
		require.True(t, success, "Process pod should be deleted")
	}
}

// TestMultipleCRDWorkloadTypes tests handling of multiple CRD workload types
func TestMultipleCRDWorkloadTypes(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a fake dynamic client for testing CRDs
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme)

	// Create CRD resources for different workload types
	createTestProcessCRD(t, dynamicClient, env.namespace)
	createTestCronJobCRD(t, dynamicClient, env.namespace)
	createTestServiceCRD(t, dynamicClient, env.namespace)

	// Create config directory
	tempDir, err := os.MkdirTemp("", "multi-crd-configs")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	clusterConfig := `
apiVersion: v1
clusters:
- cluster:
    server: https://kubernetes.docker.internal:6443
  name: default
`
	clusterPath := filepath.Join(tempDir, "cluster-config.yaml")
	err = os.WriteFile(clusterPath, []byte(clusterConfig), 0644)
	require.NoError(t, err)

	// Create a config file with multiple workload types referencing CRDs
	multiConfigYAML := fmt.Sprintf(`
id: ""
tenant: "test-tenant"
port: 8080
namespace: "default"

storageType: "redis"
redis:
  addr: "10.1.1.1:6379"
  password: ""
  db: 0

cluster:
  name: "default"
  kubeconfig: "%s"

workloads:
  processes:
    - name: crd-process
      namespace: %s
      image: busybox:latest
      script:
        commands:
          - echo 'Fallback script'
        shell: /bin/sh
      workloadCRDRef:
        apiVersion: highlander.plexobject.io/v1
        kind: WorkloadProcess
        name: test-process-crd
        namespace: %s

  cronJobs:
    - name: crd-cronjob
      namespace: %s
      image: busybox:latest
      script:
        commands:
          - echo 'Fallback script'
        shell: /bin/sh
      schedule: "*/30 * * * *"
      workloadCRDRef:
        apiVersion: highlander.plexobject.io/v1
        kind: WorkloadCronJob
        name: test-cronjob-crd
        namespace: %s

  services:
    - name: crd-service
      namespace: %s
      image: nginx:latest
      replicas: 1
      script:
        commands:
          - nginx -g 'daemon off;'
        shell: /bin/sh
      workloadCRDRef:
        apiVersion: highlander.plexobject.io/v1
        kind: WorkloadService
        name: test-service-crd
        namespace: %s
`, clusterPath, env.namespace, env.namespace, env.namespace, env.namespace, env.namespace, env.namespace)

	configFile := filepath.Join(tempDir, "multi-crd-config.yaml")
	err = os.WriteFile(configFile, []byte(multiConfigYAML), 0644)
	require.NoError(t, err)

	// Create workload manager
	workloadMgr, err := env.NewManager(true)
	require.NoError(t, err)

	// Create a controller
	controller := NewLeaderController(
		env.leaderStorage,
		"multi-crd-controller",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"tenant",
		false,
	)

	// Create AppConfig
	appConfig, err := common.InitConfig(configFile)
	require.NoError(t, err, "appconfig %s", appConfig)

	// TODO Inject dynamic client for testing

	// Start the controller
	err = controller.Start(ctx)
	require.NoError(t, err)

	// Wait for leadership
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// In real mode, verify all workload types were created with CRD configuration
	if env.mode == RealMode {
		// Check process pod
		processSuccess := env.waitForPodState(t, "crd-process-pod", true, 15*time.Second)
		require.True(t, processSuccess, "Process pod from CRD should be running")

		// Check cronjob
		cronSuccess := env.waitForCronJob(t, "crd-cronjob", false, 15*time.Second)
		require.True(t, cronSuccess, "CronJob from CRD should be created and not suspended")

		// Check service
		serviceSuccess := env.waitForDeployment(t, "crd-service", 2, 15*time.Second)
		require.True(t, serviceSuccess, "Service from CRD should have 2 replicas as specified in CRD")
	}

	// In real mode, verify the initial pod was created with CRD configuration
	var initialPodCreationTime metav1.Time
	if env.mode == RealMode {
		// Wait for the pod to be created
		success := env.waitForPodState(t, "updatable-process-pod", true, 15*time.Second)
		require.True(t, success, "Process pod should be running")

		// Get the pod to verify initial configuration
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "updatable-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
		initialPodCreationTime = pod.CreationTimestamp

		// Check initial image from CRD
		require.Equal(t, "busybox:crd-version", pod.Spec.Containers[0].Image, "Pod should use initial image from CRD")

		// Check initial environment variable
		foundCRDEnv := false
		for _, env := range pod.Spec.Containers[0].Env {
			if env.Name == "CRD_CONFIG" && env.Value == "from-crd" {
				foundCRDEnv = true
				break
			}
		}
		require.True(t, foundCRDEnv, "Initial CRD environment variable should be present")
	}

	// Update the CRD
	updateTestProcessCRD(t, dynamicClient, env.namespace)

	// Wait for the controller to detect the update
	time.Sleep(5 * time.Second)

	// In real mode, verify the pod was updated with new CRD configuration
	if env.mode == RealMode {
		// Verify pod was recreated with updated configuration
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "updatable-process-pod", metav1.GetOptions{})
		require.NoError(t, err)

		// The pod should be recreated when CRD is updated
		require.True(t, pod.CreationTimestamp.After(initialPodCreationTime.Time),
			"Pod should be recreated after CRD update")

		// Check updated image
		require.Equal(t, "busybox:updated", pod.Spec.Containers[0].Image, "Pod should use updated image from CRD")

		// Check updated environment variable
		foundUpdatedEnv := false
		for _, env := range pod.Spec.Containers[0].Env {
			if env.Name == "CRD_CONFIG" && env.Value == "updated-from-crd" {
				foundUpdatedEnv = true
				break
			}
		}
		require.True(t, foundUpdatedEnv, "Updated CRD environment variable should be present")
	}

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)

	// In real mode, verify pod was deleted
	if env.mode == RealMode {
		success := env.waitForPodState(t, "updatable-process-pod", false, 15*time.Second)
		require.True(t, success, "Process pod should be deleted")
	}

	// In real mode, verify all workload types were created with CRD configuration
	if env.mode == RealMode {
		// Check process pod
		processSuccess := env.waitForPodState(t, "crd-process-pod", true, 15*time.Second)
		require.True(t, processSuccess, "Process pod from CRD should be running")

		// Check cronjob
		cronSuccess := env.waitForCronJob(t, "crd-cronjob", false, 15*time.Second)
		require.True(t, cronSuccess, "CronJob from CRD should be created and not suspended")

		// Check service
		serviceSuccess := env.waitForDeployment(t, "crd-service", 2, 15*time.Second)
		require.True(t, serviceSuccess, "Service from CRD should have 2 replicas as specified in CRD")
	}

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)

	// In real mode, verify all workloads were stopped
	if env.mode == RealMode {
		// Check process pod deletion
		processSuccess := env.waitForPodState(t, "crd-process-pod", false, 15*time.Second)
		require.True(t, processSuccess, "Process pod should be deleted")

		// Check cronjob suspended
		cronJob, err := env.GetClient().BatchV1().CronJobs(env.namespace).Get(ctx, "crd-cronjob", metav1.GetOptions{})
		if err == nil {
			require.True(t, *cronJob.Spec.Suspend, "CronJob should be suspended")
		}

		// Check service scaled down
		serviceSuccess := env.waitForDeployment(t, "crd-service", 0, 15*time.Second)
		require.True(t, serviceSuccess, "Service should be scaled to 0")
	}
}

// TestCRDConfigUpdate tests updating CRD-based workload configurations
func TestCRDConfigUpdate(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a fake dynamic client for testing CRDs
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme)

	// Create initial CRD resource
	createTestProcessCRD(t, dynamicClient, env.namespace)

	// Create config directory
	tempDir, err := os.MkdirTemp("", "crd-update-configs")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create config file referencing the CRD
	clusterConfig := `
apiVersion: v1
clusters:
- cluster:
    server: https://kubernetes.docker.internal:6443
  name: default
`
	clusterPath := filepath.Join(tempDir, "cluster-config.yaml")
	err = os.WriteFile(clusterPath, []byte(clusterConfig), 0644)
	require.NoError(t, err)

	// Create a config file with multiple workload types referencing CRDs
	configYAML := fmt.Sprintf(`
id: ""
tenant: "test-tenant"
port: 8080
namespace: "default"

storageType: "redis"
redis:
  addr: "10.1.1.1:6379"
  password: ""
  db: 0

cluster:
  name: "default"
  kubeconfig: "%s"

workloads:
  processes:
    - name: updatable-process
      namespace: %s
      image: busybox:latest
      script:
        commands:
          - echo 'Fallback script'
        shell: /bin/sh
      workloadCRDRef:
        apiVersion: highlander.plexobject.io/v1
        kind: WorkloadProcess
        name: test-process-crd
        namespace: %s

`, clusterPath, env.namespace, env.namespace)

	configFile := filepath.Join(tempDir, "update-crd-config.yaml")
	err = os.WriteFile(configFile, []byte(configYAML), 0644)
	require.NoError(t, err)

	// Create workload manager
	workloadMgr, err := env.NewManager(true)
	require.NoError(t, err)

	// Create a controller
	controller := NewLeaderController(
		env.leaderStorage,
		"crd-update-controller",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"tenant",
		false,
	)

	// Create AppConfig
	appConfig, err := common.InitConfig(configFile)
	require.NoError(t, err, "appconfig %s", appConfig)

	// TODO Inject dynamic client for testing

	// Start the controller
	err = controller.Start(ctx)
	require.NoError(t, err)

	// Wait for leadership
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// In real mode, verify the initial pod was created with CRD configuration
	var initialPodCreationTime metav1.Time
	if env.mode == RealMode {
		// Wait for the pod to be created
		success := env.waitForPodState(t, "updatable-process-pod", true, 15*time.Second)
		require.True(t, success, "Process pod should be running")

		// Get the pod to verify initial configuration
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "updatable-process-pod", metav1.GetOptions{})
		require.NoError(t, err)
		initialPodCreationTime = pod.CreationTimestamp

		// Check initial image from CRD
		require.Equal(t, "busybox:crd-version", pod.Spec.Containers[0].Image, "Pod should use initial image from CRD")

		// Check initial environment variable
		foundCRDEnv := false
		for _, env := range pod.Spec.Containers[0].Env {
			if env.Name == "CRD_CONFIG" && env.Value == "from-crd" {
				foundCRDEnv = true
				break
			}
		}
		require.True(t, foundCRDEnv, "Initial CRD environment variable should be present")
	}

	// Update the CRD
	updateTestProcessCRD(t, dynamicClient, env.namespace)

	// Wait for the controller to detect the update
	time.Sleep(5 * time.Second)

	// In real mode, verify the pod was updated with new CRD configuration
	if env.mode == RealMode {
		// Verify pod was recreated with updated configuration
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "updatable-process-pod", metav1.GetOptions{})
		require.NoError(t, err)

		// The pod should be recreated when CRD is updated
		require.True(t, pod.CreationTimestamp.After(initialPodCreationTime.Time),
			"Pod should be recreated after CRD update")

		// Check updated image
		require.Equal(t, "busybox:updated", pod.Spec.Containers[0].Image, "Pod should use updated image from CRD")

		// Check updated environment variable
		foundUpdatedEnv := false
		for _, env := range pod.Spec.Containers[0].Env {
			if env.Name == "CRD_CONFIG" && env.Value == "updated-from-crd" {
				foundUpdatedEnv = true
				break
			}
		}
		require.True(t, foundUpdatedEnv, "Updated CRD environment variable should be present")
	}

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)

	// In real mode, verify pod was deleted
	if env.mode == RealMode {
		success := env.waitForPodState(t, "updatable-process-pod", false, 15*time.Second)
		require.True(t, success, "Process pod should be deleted")
	}
}

// Helper function to create a test process CRD
func createTestProcessCRD(t *testing.T, client *fake.FakeDynamicClient, namespace string) {
	processCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadProcess",
			"metadata": map[string]interface{}{
				"name":      "test-process-crd",
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

	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	_, err := client.Resource(gvr).Namespace(namespace).Create(context.Background(), processCRD, metav1.CreateOptions{})
	require.NoError(t, err)
}

// Helper function to update a process CRD with new values
func updateTestProcessCRD(t *testing.T, client *fake.FakeDynamicClient, namespace string) {
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	// Get the existing CRD
	existingCRD, err := client.Resource(gvr).Namespace(namespace).Get(context.Background(), "test-process-crd", metav1.GetOptions{})
	require.NoError(t, err)

	// Update the CRD
	spec := existingCRD.Object["spec"].(map[string]interface{})
	spec["image"] = "busybox:updated"
	spec["env"].(map[string]interface{})["CRD_CONFIG"] = "updated-from-crd"

	// Update commands
	script := spec["script"].(map[string]interface{})
	commands := []interface{}{
		"echo 'Starting UPDATED CRD-defined process'",
		"echo 'Process ID: $$'",
		"sleep 300",
	}
	script["commands"] = commands

	// Save the updated CRD
	_, err = client.Resource(gvr).Namespace(namespace).Update(context.Background(), existingCRD, metav1.UpdateOptions{})
	require.NoError(t, err)
}

// Helper function to create a test cronjob CRD
func createTestCronJobCRD(t *testing.T, client *fake.FakeDynamicClient, namespace string) {
	cronJobCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadCronJob",
			"metadata": map[string]interface{}{
				"name":      "test-cronjob-crd",
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

	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.CronjobsResource,
	}

	_, err := client.Resource(gvr).Namespace(namespace).Create(context.Background(), cronJobCRD, metav1.CreateOptions{})
	require.NoError(t, err)
}

// Helper function to create a test service CRD
func createTestServiceCRD(t *testing.T, client *fake.FakeDynamicClient, namespace string) {
	serviceCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": common.GROUP + "/" + common.MajorVersion,
			"kind":       "WorkloadService",
			"metadata": map[string]interface{}{
				"name":      "test-service-crd",
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

	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ServicesResource,
	}

	_, err := client.Resource(gvr).Namespace(namespace).Create(context.Background(), serviceCRD, metav1.CreateOptions{})
	require.NoError(t, err)
}
