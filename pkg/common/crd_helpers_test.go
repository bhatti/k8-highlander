package common

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestCreateProcessCRD(t *testing.T) {
	// Create test schema and dynamic client
	scheme := CreateTestScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

	// Test data
	ctx := context.Background()
	name := "test-process"
	namespace := "default"
	image := "busybox:latest"
	commands := []string{"echo", "hello"}
	shell := "/bin/sh"
	env := map[string]string{"KEY": "value"}
	resources := ResourceConfig{
		CPURequest:    "100m",
		MemoryRequest: "64Mi",
	}
	restartPolicy := "Never"
	maxRestarts := 3
	gracePeriod := "30s"

	// Create process CRD
	err := CreateProcessCRD(ctx, dynamicClient, name, namespace, image, commands, shell, env, resources, restartPolicy, maxRestarts, gracePeriod)
	require.NoError(t, err)

	// Verify it was created correctly
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: ProcessesResource,
	}

	obj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)

	// Check basic metadata
	assert.Equal(t, name, obj.GetName())
	assert.Equal(t, namespace, obj.GetNamespace())

	// Check spec fields
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	require.NoError(t, err)
	require.True(t, found, "Spec not found in object")

	assert.Equal(t, image, spec["image"])

	// Check script
	script, found, err := unstructured.NestedMap(spec, "script")
	require.NoError(t, err)
	require.True(t, found, "Script not found in spec")

	cmds, found, err := unstructured.NestedSlice(script, "commands")
	require.NoError(t, err)
	require.True(t, found, "Commands not found in script")
	assert.Equal(t, commands[0], cmds[0])
	assert.Equal(t, commands[1], cmds[1])

	// Check environment variables
	envMap, found, err := unstructured.NestedMap(spec, "env")
	require.NoError(t, err)
	require.True(t, found, "Env not found in spec")
	assert.Equal(t, "value", envMap["KEY"])

	// Check restart policy and max restarts
	assert.Equal(t, restartPolicy, spec["restartPolicy"])
	assert.Equal(t, int64(maxRestarts), spec["maxRestarts"])
}

func TestCreateServiceCRD(t *testing.T) {
	// Create test schema and dynamic client
	scheme := CreateTestScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

	// Test data
	ctx := context.Background()
	name := "test-service"
	namespace := "default"
	image := "nginx:alpine"
	commands := []string{"nginx", "-g", "daemon off;"}
	shell := "/bin/sh"
	replicas := int32(2)
	ports := []PortConfig{
		{
			Name:          "http",
			ContainerPort: 80,
			ServicePort:   8080,
		},
	}
	env := map[string]string{"KEY": "value"}
	resources := ResourceConfig{
		CPURequest:    "100m",
		MemoryRequest: "64Mi",
	}
	healthCheckPath := "/health"
	healthCheckPort := int32(80)

	// Create service CRD
	err := CreateServiceCRD(ctx, dynamicClient, name, namespace, image, commands, shell, replicas, ports, env, resources, healthCheckPath, healthCheckPort)
	require.NoError(t, err)

	// Verify it was created correctly
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: ServicesResource,
	}

	obj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)

	// Check basic metadata
	assert.Equal(t, name, obj.GetName())

	// Check spec fields
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	require.NoError(t, err)
	require.True(t, found, "Spec not found in object")

	assert.Equal(t, image, spec["image"])
	assert.Equal(t, int64(replicas), spec["replicas"])
	assert.Equal(t, healthCheckPath, spec["healthCheckPath"])
	assert.Equal(t, int64(healthCheckPort), spec["healthCheckPort"])

	// Check ports
	portsArray, found, err := unstructured.NestedSlice(spec, "ports")
	require.NoError(t, err)
	require.True(t, found, "Ports not found in spec")
	assert.Len(t, portsArray, 1)

	port, ok := portsArray[0].(map[string]interface{})
	require.True(t, ok, "Port is not a map")
	assert.Equal(t, "http", port["name"])
	assert.Equal(t, int64(80), port["containerPort"])
	assert.Equal(t, int64(8080), port["servicePort"])
}

func TestCreateCronJobCRD(t *testing.T) {
	// Create test schema and dynamic client
	scheme := CreateTestScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

	// Test data
	ctx := context.Background()
	name := "test-cronjob"
	namespace := "default"
	image := "busybox:latest"
	commands := []string{"echo", "hello"}
	shell := "/bin/sh"
	schedule := "*/5 * * * *"
	env := map[string]string{"KEY": "value"}
	resources := ResourceConfig{
		CPURequest:    "100m",
		MemoryRequest: "64Mi",
	}
	restartPolicy := "OnFailure"

	// Create cron job CRD
	err := CreateCronJobCRD(ctx, dynamicClient, name, namespace, image, commands, shell, schedule, env, resources, restartPolicy)
	require.NoError(t, err)

	// Verify it was created correctly
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: CronjobsResource,
	}

	obj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)

	// Check spec fields
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	require.NoError(t, err)
	require.True(t, found, "Spec not found in object")

	assert.Equal(t, image, spec["image"])
	assert.Equal(t, schedule, spec["schedule"])
	assert.Equal(t, restartPolicy, spec["restartPolicy"])
}

func TestCreatePersistentCRD(t *testing.T) {
	// Create test schema and dynamic client
	scheme := CreateTestScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

	// Test data
	ctx := context.Background()
	name := "test-persistent"
	namespace := "default"
	image := "redis:alpine"
	commands := []string{"redis-server"}
	shell := "/bin/sh"
	replicas := int32(1)
	ports := []PortConfig{
		{
			Name:          "redis",
			ContainerPort: 6379,
			ServicePort:   6379,
		},
	}
	env := map[string]string{"KEY": "value"}
	resources := ResourceConfig{
		CPURequest:    "100m",
		MemoryRequest: "128Mi",
	}
	serviceName := "test-redis"
	healthCheckPath := "/health"
	healthCheckPort := int32(6379)
	persistentVolumes := []VolumeConfig{
		{
			Name:      "data",
			MountPath: "/data",
			Size:      "1Gi",
		},
	}

	// Create persistent CRD
	err := CreatePersistentCRD(ctx, dynamicClient, name, namespace, image, commands, shell, replicas, ports, env, resources, serviceName, healthCheckPath, healthCheckPort, persistentVolumes)
	require.NoError(t, err)

	// Verify it was created correctly
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: PersistentsResource,
	}

	obj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)

	// Check spec fields
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	require.NoError(t, err)
	require.True(t, found, "Spec not found in object")

	assert.Equal(t, image, spec["image"])
	assert.Equal(t, int64(replicas), spec["replicas"])
	assert.Equal(t, serviceName, spec["serviceName"])

	// Check persistent volumes
	volumes, found, err := unstructured.NestedSlice(spec, "persistentVolumes")
	require.NoError(t, err)
	require.True(t, found, "PersistentVolumes not found in spec")
	assert.Len(t, volumes, 1)

	volume, ok := volumes[0].(map[string]interface{})
	require.True(t, ok, "Volume is not a map")
	assert.Equal(t, "data", volume["name"])
	assert.Equal(t, "/data", volume["mountPath"])
	assert.Equal(t, "1Gi", volume["size"])
}

// TestResourceConfigToMap tests the resourceConfigToMap helper function
func TestResourceConfigToMap(t *testing.T) {
	resources := ResourceConfig{
		CPURequest:    "100m",
		MemoryRequest: "64Mi",
		CPULimit:      "200m",
		MemoryLimit:   "128Mi",
	}

	resourceMap := resourceConfigToMap(resources)

	assert.Equal(t, "100m", resourceMap["cpuRequest"])
	assert.Equal(t, "64Mi", resourceMap["memoryRequest"])
	assert.Equal(t, "200m", resourceMap["cpuLimit"])
	assert.Equal(t, "128Mi", resourceMap["memoryLimit"])
}
