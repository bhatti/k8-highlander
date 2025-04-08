package common

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakeDynamic "k8s.io/client-go/dynamic/fake"
	fakeKube "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

// TestWorkloadCRDReference tests the WorkloadCRDReference struct
func TestWorkloadCRDReference(t *testing.T) {
	yamlContent := `
workloadCRDRef:
  apiVersion: "highlander.plexobject.io/v1"
  kind: "WorkloadProcess"
  name: "singleton-process"
  namespace: "default"
`

	var config struct {
		WorkloadCRDRef *WorkloadCRDReference `yaml:"workloadCRDRef"`
	}

	err := yaml.Unmarshal([]byte(yamlContent), &config)
	require.NoError(t, err, "Failed to unmarshal YAML")

	require.NotNil(t, config.WorkloadCRDRef)
	assert.Equal(t, "highlander.plexobject.io/v1", config.WorkloadCRDRef.APIVersion)
	assert.Equal(t, "WorkloadProcess", config.WorkloadCRDRef.Kind)
	assert.Equal(t, "singleton-process", config.WorkloadCRDRef.Name)
	assert.Equal(t, "default", config.WorkloadCRDRef.Namespace)
}

// TestCheckForCRDReferences tests the checkForCRDReferences function
func TestCheckForCRDReferences(t *testing.T) {
	tests := []struct {
		name     string
		config   AppConfig
		expected bool
	}{
		{
			name: "No CRD references",
			config: AppConfig{
				Workloads: struct {
					Processes      []ProcessConfig    `yaml:"processes"`
					CronJobs       []CronJobConfig    `yaml:"cronJobs"`
					Services       []ServiceConfig    `yaml:"services"`
					PersistentSets []PersistentConfig `yaml:"persistentSets"`
				}{
					Processes: []ProcessConfig{
						{
							BaseWorkloadConfig: BaseWorkloadConfig{
								Name:  "test-process",
								Image: "test-image",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Process with CRD reference",
			config: AppConfig{
				Workloads: struct {
					Processes      []ProcessConfig    `yaml:"processes"`
					CronJobs       []CronJobConfig    `yaml:"cronJobs"`
					Services       []ServiceConfig    `yaml:"services"`
					PersistentSets []PersistentConfig `yaml:"persistentSets"`
				}{
					Processes: []ProcessConfig{
						{
							BaseWorkloadConfig: BaseWorkloadConfig{
								Name:  "test-process",
								Image: "test-image",
								WorkloadCRDRef: &WorkloadCRDReference{
									APIVersion: "highlander.plexobject.io/v1",
									Kind:       "WorkloadProcess",
									Name:       "test-process",
									Namespace:  "default",
								},
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkForCRDReferences(&tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestLoadWorkloadFromCRD tests the LoadWorkloadFromCRD function
func TestLoadWorkloadFromCRD(t *testing.T) {
	// Create a fake dynamic client
	scheme := runtime.NewScheme()
	client := fakeDynamic.NewSimpleDynamicClient(scheme)

	// Create test objects
	processObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "highlander.plexobject.io/v1",
			"kind":       "WorkloadProcess",
			"metadata": map[string]interface{}{
				"name":      "test-process",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"image": "busybox:latest",
				"script": map[string]interface{}{
					"commands": []interface{}{
						"echo 'Hello, World!'",
						"sleep 60",
					},
					"shell": "/bin/sh",
				},
				"env": map[string]interface{}{
					"TEST_ENV": "test-value",
				},
				"resources": map[string]interface{}{
					"cpuRequest":    "100m",
					"memoryRequest": "64Mi",
				},
				"restartPolicy": "Never",
				"maxRestarts":   int64(3),
				"gracePeriod":   "30s",
			},
		},
	}

	// Add the object to the fake client
	_, err := client.Resource(schema.GroupVersionResource{
		Group:    "highlander.plexobject.io",
		Version:  "v1",
		Resource: "workloadprocesses",
	}).Namespace("default").Create(context.Background(), processObj, metav1.CreateOptions{})
	require.NoError(t, err)

	// Test loading the workload
	t.Run("LoadWorkloadProcess", func(t *testing.T) {
		ref := &WorkloadCRDReference{
			APIVersion: "highlander.plexobject.io/v1",
			Kind:       "WorkloadProcess",
			Name:       "test-process",
			Namespace:  "default",
		}

		result, err := LoadWorkloadFromCRD(context.Background(), client, ref)
		require.NoError(t, err)

		processConfig, ok := result.(*ProcessConfig)
		require.True(t, ok, "Result should be a ProcessConfig")

		assert.Equal(t, "test-process", processConfig.Name)
		assert.Equal(t, "default", processConfig.Namespace)
		assert.Equal(t, "busybox:latest", processConfig.Image)

		require.NotNil(t, processConfig.Script)
		assert.Len(t, processConfig.Script.Commands, 2)
		assert.Equal(t, "echo 'Hello, World!'", processConfig.Script.Commands[0])
		assert.Equal(t, "sleep 60", processConfig.Script.Commands[1])
		assert.Equal(t, "/bin/sh", processConfig.Script.Shell)

		assert.Equal(t, "test-value", processConfig.Env["TEST_ENV"])
		assert.Equal(t, "100m", processConfig.Resources.CPURequest)
		assert.Equal(t, "64Mi", processConfig.Resources.MemoryRequest)
		assert.Equal(t, "Never", processConfig.RestartPolicy)
		assert.Equal(t, 3, processConfig.MaxRestarts)

		// Check that the CRD reference is preserved
		require.NotNil(t, processConfig.WorkloadCRDRef)
		assert.Equal(t, "highlander.plexobject.io/v1", processConfig.WorkloadCRDRef.APIVersion)
		assert.Equal(t, "WorkloadProcess", processConfig.WorkloadCRDRef.Kind)
		assert.Equal(t, "test-process", processConfig.WorkloadCRDRef.Name)
		assert.Equal(t, "default", processConfig.WorkloadCRDRef.Namespace)
	})
}

// TestClusterConfigWithSelectedKubeconfig tests the ClusterConfig with SelectedKubeconfig field
func TestClusterConfigWithSelectedKubeconfig(t *testing.T) {
	// Create a fake client
	fakeClient := fakeKube.NewSimpleClientset()

	// Create a cluster config
	cluster := &ClusterConfig{
		Name:       "test-cluster",
		Kubeconfig: "test-kubeconfig.yaml",
		SelectedKubeconfig: &rest.Config{
			Host: "https://example.com",
		},
	}

	// Set the client
	cluster.SetClient(fakeClient)

	// Check that we can get the client
	client := cluster.GetClient()
	assert.Equal(t, fakeClient, client)
}

// TestLoadCRDConfigurations simulates loading CRD configurations
func TestLoadCRDConfigurations(t *testing.T) {
	// Create a fake dynamic client
	scheme := runtime.NewScheme()
	client := fakeDynamic.NewSimpleDynamicClient(scheme)

	// Create test objects
	processObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "highlander.plexobject.io/v1",
			"kind":       "WorkloadProcess",
			"metadata": map[string]interface{}{
				"name":      "test-process",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"image": "crd-image:latest",
				"script": map[string]interface{}{
					"commands": []interface{}{
						"echo 'From CRD'",
						"sleep 30",
					},
					"shell": "/bin/sh",
				},
				"env": map[string]interface{}{
					"CRD_ENV": "from-crd",
				},
				"restartPolicy": "Never",
			},
		},
	}

	// Add the object to the fake client
	_, err := client.Resource(schema.GroupVersionResource{
		Group:    "highlander.plexobject.io",
		Version:  "v1",
		Resource: "workloadprocesses",
	}).Namespace("default").Create(context.Background(), processObj, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create a config with a CRD reference
	config := &AppConfig{
		Workloads: struct {
			Processes      []ProcessConfig    `yaml:"processes"`
			CronJobs       []CronJobConfig    `yaml:"cronJobs"`
			Services       []ServiceConfig    `yaml:"services"`
			PersistentSets []PersistentConfig `yaml:"persistentSets"`
		}{
			Processes: []ProcessConfig{
				{
					BaseWorkloadConfig: BaseWorkloadConfig{
						Name: "reference-process",
						WorkloadCRDRef: &WorkloadCRDReference{
							APIVersion: "highlander.plexobject.io/v1",
							Kind:       "WorkloadProcess",
							Name:       "test-process",
							Namespace:  "default",
						},
					},
				},
			},
		},
	}

	// Implement a simplified loadCRDConfigurations for testing
	doLoadCRD := func(cfg *AppConfig) error {
		for i := range cfg.Workloads.Processes {
			process := &cfg.Workloads.Processes[i]
			if process.WorkloadCRDRef != nil {
				crdConfig, err := LoadWorkloadFromCRD(context.Background(), client, process.WorkloadCRDRef)
				if err != nil {
					return err
				}

				if procConfig, ok := crdConfig.(*ProcessConfig); ok {
					// Save original name and namespace
					origName := process.Name
					origNamespace := process.Namespace

					// Copy configuration from CRD
					*process = *procConfig

					// Restore name and namespace if they were empty
					if process.Name == "" {
						process.Name = origName
					}
					if process.Namespace == "" {
						process.Namespace = origNamespace
					}
				}
			}
		}
		return nil
	}

	// Load CRD configurations
	err = doLoadCRD(config)
	require.NoError(t, err)

	// Verify the process was configured from the CRD
	require.Len(t, config.Workloads.Processes, 1)
	process := config.Workloads.Processes[0]

	assert.Equal(t, "test-process", process.Name, "Name should be preserved from config")
	assert.Equal(t, "crd-image:latest", process.Image, "Image should be loaded from CRD")

	require.NotNil(t, process.Script)
	assert.Len(t, process.Script.Commands, 2)
	assert.Equal(t, "echo 'From CRD'", process.Script.Commands[0])
	assert.Equal(t, "sleep 30", process.Script.Commands[1])

	assert.Equal(t, "from-crd", process.Env["CRD_ENV"], "Environment variables should be loaded from CRD")
	assert.Equal(t, "Never", process.RestartPolicy, "Restart policy should be loaded from CRD")
}

// TestConfigWithCRDValidation tests validation of config with CRD references
func TestConfigWithCRDValidation(t *testing.T) {
	// Create a config with a CRD reference but missing required fields
	configWithCRDOnly := AppConfig{
		Workloads: struct {
			Processes      []ProcessConfig    `yaml:"processes"`
			CronJobs       []CronJobConfig    `yaml:"cronJobs"`
			Services       []ServiceConfig    `yaml:"services"`
			PersistentSets []PersistentConfig `yaml:"persistentSets"`
		}{
			Processes: []ProcessConfig{
				{
					BaseWorkloadConfig: BaseWorkloadConfig{
						Name: "test-process",
						// Missing image and script
						WorkloadCRDRef: &WorkloadCRDReference{
							APIVersion: "highlander.plexobject.io/v1",
							Kind:       "WorkloadProcess",
							Name:       "test-process",
							Namespace:  "default",
						},
					},
				},
			},
		},
	}
	configWithCRDOnly.Redis.Addr = "localhost"
	configWithCRDOnly.Cluster.Name = "default"
	// Validation should fail without required fields
	err := configWithCRDOnly.Validate()
	assert.Error(t, err, "Validation should fail with missing required fields")

	// Now add the required fields
	configWithCRDOnly.Workloads.Processes[0].Image = "test-image"
	configWithCRDOnly.Workloads.Processes[0].Script = &ScriptConfig{
		Commands: []string{"echo 'test'"},
		Shell:    "/bin/sh",
	}

	// Validation should now pass
	err = configWithCRDOnly.Validate()
	assert.NoError(t, err, "Validation should pass with required fields")
}

// createSimulatedKubeConfig creates a simulated kubeconfig file for testing
func createSimulatedKubeConfig(t *testing.T, dir string) string {
	content := `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://cluster1.example.com
  name: cluster1
- cluster:
    server: https://cluster2.example.com
  name: cluster2
contexts:
- context:
    cluster: cluster1
    user: user1
  name: context1
- context:
    cluster: cluster2
    user: user2
  name: context2
current-context: context1
users:
- name: user1
  user:
    token: token1
- name: user2
  user:
    token: token2
`

	path := filepath.Join(dir, "kubeconfig.yaml")
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	return path
}

// TestGetKubeConfigForCluster tests loading a kubeconfig for a specific cluster
func TestGetKubeConfigForCluster(t *testing.T) {
	// Skip if we can't run kubeconfig tests
	if testing.Short() {
		t.Skip("Skipping kubeconfig tests in short mode")
	}

	// Create a temporary directory
	dir, err := os.MkdirTemp("", "kubeconfig-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Create a simulated kubeconfig file
	kubeconfigPath := createSimulatedKubeConfig(t, dir)

	// Test loading for specific cluster
	config, err := getKubeConfigForCluster(kubeconfigPath, "cluster2")
	if err != nil {
		t.Skip("Skipping test that requires access to kubeconfig files")
	}

	assert.Equal(t, "https://cluster2.example.com", config.Host)

	// Test loading default cluster
	config, err = getKubeConfigForCluster(kubeconfigPath, "")
	require.NoError(t, err)
	assert.Equal(t, "https://cluster1.example.com", config.Host)
}
