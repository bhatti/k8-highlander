package leader

import (
	"context"
	"encoding/json"
	ierrors "errors"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"github.com/bhatti/k8-highlander/pkg/storage"
	"github.com/bhatti/k8-highlander/pkg/workloads"
	"github.com/bhatti/k8-highlander/pkg/workloads/cronjob"
	"github.com/bhatti/k8-highlander/pkg/workloads/process"
	"github.com/bhatti/k8-highlander/pkg/workloads/service"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

// TestMode determines whether to use real or mock components
type TestMode string

const (
	MockMode TestMode = "mock"
	RealMode TestMode = "real"
)

// StorageType determines which storage implementation to use
type StorageType string

const (
	RedisStorage StorageType = "redis"
	DBStorage    StorageType = "db"
)

// TestEnv encapsulates the test environment
type TestEnv struct {
	t               *testing.T
	namespace       string
	mode            TestMode
	storageType     StorageType
	redisServer     *miniredis.Miniredis
	redisClient     *redis.Client
	dbClient        *gorm.DB
	leaderStorage   storage.LeaderStorage
	metrics         *monitoring.ControllerMetrics
	monitorServer   *monitoring.MonitoringServer
	controllers     map[string]*LeaderController
	cluster         common.ClusterConfig
	mockWorkloadMgr *MockWorkloadManager
}

// MockWorkloadManager is a mock implementation of workloads.Manager
type MockWorkloadManager struct {
	mu             sync.RWMutex
	startCalls     int
	stopCalls      int
	activeWorkload bool
	startError     error
	stopError      error
	workloads      map[string]common.Workload
}

func (m *MockWorkloadManager) Cleanup() {
}

// Ensure MockWorkloadManager implements workloads.Manager
var _ workloads.Manager = (*MockWorkloadManager)(nil)

func NewMockWorkloadManager() *MockWorkloadManager {
	return &MockWorkloadManager{
		workloads: make(map[string]common.Workload),
	}
}

func (e *TestEnv) GetClient() kubernetes.Interface {
	return e.cluster.GetClient()
}

func (e *TestEnv) SetClient(client kubernetes.Interface) {
	e.cluster.SetClient(client)
}

func (m *MockWorkloadManager) StartAll(ctx context.Context, client kubernetes.Interface) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.startCalls++
	m.activeWorkload = true

	// Start all workloads
	for _, workload := range m.workloads {
		if err := workload.Start(ctx, client); err != nil {
			return err
		}
	}
	return m.startError
}

func (m *MockWorkloadManager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stopCalls++
	m.activeWorkload = false

	// Stop all workloads
	for _, workload := range m.workloads {
		if err := workload.Stop(ctx); err != nil {
			return err
		}
	}
	return m.stopError
}

func (m *MockWorkloadManager) AddWorkload(name string, workload common.Workload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workloads[name] = workload
	return nil
}

func (m *MockWorkloadManager) RemoveWorkload(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.workloads, name)
	return nil
}

func (m *MockWorkloadManager) GetWorkload(name string) (common.Workload, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workloads[name]
	return w, ok
}

func (m *MockWorkloadManager) GetAllWorkloads() map[string]common.Workload {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]common.Workload)
	for k, v := range m.workloads {
		result[k] = v
	}
	return result
}

func (m *MockWorkloadManager) GetAllStatuses() map[string]common.WorkloadStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]common.WorkloadStatus)
	for k, v := range m.workloads {
		result[k] = v.GetStatus()
	}
	return result
}

func (m *MockWorkloadManager) GetStartCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startCalls
}

func (m *MockWorkloadManager) GetStopCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopCalls
}

func (m *MockWorkloadManager) IsActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeWorkload
}

func (m *MockWorkloadManager) SetStartError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startError = err
}

func (m *MockWorkloadManager) SetStopError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopError = err
}

// MockWorkload is a mock implementation of workloads.Workload
type MockWorkload struct {
	name    string
	wtype   common.WorkloadType
	status  common.WorkloadStatus
	started bool
	stopped bool
	mutex   sync.Mutex
}

func NewMockWorkload(name string, wtype common.WorkloadType) *MockWorkload {
	return &MockWorkload{
		name:  name,
		wtype: wtype,
		status: common.WorkloadStatus{
			Name:    name,
			Type:    wtype,
			Active:  false, // Initially not active
			Healthy: true,
			Details: make(map[string]interface{}),
		},
		mutex: sync.Mutex{},
	}
}

func (w *MockWorkload) Start(context.Context, kubernetes.Interface) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.started = true

	// Update status to active when started
	w.status.Active = true
	w.status.LastTransition = time.Now()

	return nil
}

func (w *MockWorkload) Stop(context.Context) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.stopped = true

	// Update status to inactive when stopped
	w.status.Active = false
	w.status.LastTransition = time.Now()

	return nil
}

func (w *MockWorkload) GetStatus() common.WorkloadStatus {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	// Return a copy of the status to avoid race conditions
	status := w.status
	return status
}

func (w *MockWorkload) GetName() string {
	return w.name
}

func (w *MockWorkload) GetType() common.WorkloadType {
	return w.wtype
}

// IsStarted returns whether the workload has been started
func (w *MockWorkload) IsStarted() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.started
}

// IsStopped returns whether the workload has been stopped
func (w *MockWorkload) IsStopped() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.stopped
}

// NewTestEnv creates a new test environment
func NewTestEnv(t *testing.T, namespace string) *TestEnv {
	mode := MockMode
	if os.Getenv("HIGHLANDER_TEST_MODE") == "real" {
		mode = RealMode
	}

	storageType := RedisStorage
	if os.Getenv("HIGHLANDER_STORAGE_TYPE") == "db" {
		storageType = DBStorage
	}

	if namespace == "" {
		namespace = os.Getenv("HIGHLANDER_NAMESPACE")
	}

	if namespace == "" && mode == RealMode {
		namespace = "default"
	}
	namespace = createTestNamespace(t, namespace)

	env := &TestEnv{
		t:               t,
		mode:            mode,
		storageType:     storageType,
		namespace:       namespace,
		controllers:     make(map[string]*LeaderController),
		mockWorkloadMgr: NewMockWorkloadManager(),
	}

	// Setup storage
	if storageType == RedisStorage {
		env.setupRedisStorage()
	} else {
		env.setupDBStorage()
	}

	// Setup metrics and monitoring
	reg := prometheus.NewRegistry()

	env.metrics = monitoring.NewControllerMetrics(reg)
	env.monitorServer = monitoring.NewMonitoringServer(
		env.metrics,
		"test-version",
		"test-build",
	)

	// Add some mock workloads
	mockProcess := NewMockWorkload("test-process", common.WorkloadTypeProcess)
	mockCronJob := NewMockWorkload("test-cronjob", common.WorkloadTypeCronJob)
	_ = env.mockWorkloadMgr.AddWorkload(mockProcess.GetName(), mockProcess)
	_ = env.mockWorkloadMgr.AddWorkload(mockCronJob.GetName(), mockCronJob)

	// Setup clusters
	env.setupClusters()

	// Create the namespace if in real mode
	if env.mode == RealMode {
		_, err := env.GetClient().CoreV1().Namespaces().Create(
			context.Background(),
			&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			},
			metav1.CreateOptions{},
		)
		if err != nil && !errors.IsAlreadyExists(err) {
			t.Fatalf("Failed to create namespace %s: %v", namespace, err)
		}
		t.Logf("Created namespace: %s", namespace)
	}

	return env
}

func createTestNamespace(t *testing.T, namespace string) string {
	if namespace == "" {
		testName := t.Name()
		// Remove any characters that aren't allowed in a namespace name
		testName = strings.ToLower(testName)
		testName = strings.Replace(testName, "/", "-", -1) // Replace slashes with dashes
		testName = strings.Replace(testName, "_", "-", -1) // Replace underscores with dashes
		testName = strings.Replace(testName, ".", "-", -1) // Replace periods with dashes

		// Remove any other non-alphanumeric characters
		reg := regexp.MustCompile("[^a-z0-9-]")
		testName = reg.ReplaceAllString(testName, "")

		// Ensure it starts and ends with an alphanumeric character
		if len(testName) > 0 && !unicode.IsLetter(rune(testName[0])) && !unicode.IsDigit(rune(testName[0])) {
			testName = "t" + testName
		}
		if len(testName) > 0 && !unicode.IsLetter(rune(testName[len(testName)-1])) && !unicode.IsDigit(rune(testName[len(testName)-1])) {
			testName = testName + "t"
		}

		// Generate the namespace with a timestamp to ensure uniqueness
		namespace = fmt.Sprintf("test-%s-%d", testName, time.Now().UnixNano())
	}

	// Truncate if too long (Kubernetes limits namespace names to 63 characters)
	if len(namespace) > 63 {
		namespace = namespace[:63]
		// Ensure it still ends with an alphanumeric character
		if !unicode.IsLetter(rune(namespace[len(namespace)-1])) && !unicode.IsDigit(rune(namespace[len(namespace)-1])) {
			namespace = namespace[:62] + "t"
		}
	}
	return namespace
}

// Helper function to wait for pod to be running or deleted
func (e *TestEnv) waitForPodState(t *testing.T, name string, shouldExist bool, timeout time.Duration) bool {
	return waitForCondition(timeout, func() bool {
		pod, err := e.GetClient().CoreV1().Pods(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
		if shouldExist {
			if err != nil {
				e.LogPods()
				t.Logf("waitForPodState for Running Pod %s/%s not found yet: %v", e.namespace, name, err)
				return false
			}
			isRunning := pod.Status.Phase == corev1.PodRunning
			t.Logf("waitForPodState for Running Pod %s/%s found, phase: %s, running: %v", e.namespace, name, pod.Status.Phase, isRunning)
			if !isRunning {
				e.LogPods()
			}
			return isRunning
		} else {
			// We want the pod to be deleted
			if errors.IsNotFound(err) {
				t.Logf("Pod %s/%s is deleted (not found)", e.namespace, name)
				return true
			}

			e.LogPods()
			t.Logf("Checking if Pod is deleted further %s/%s err: %v", e.namespace, name, err)
			if err != nil {
				t.Logf("Error checking if pod %s/%s is deleted: %v", e.namespace, name, err)
				return false
			}

			// Pod exists, log its state
			t.Logf("Pod %s/%s still exists, phase: %s, deletion timestamp: %v with annotations %v, resources: %v, restart policy: %s",
				e.namespace, name, pod.Status.Phase, pod.DeletionTimestamp, pod.Annotations, pod.Spec.Resources, pod.Spec.RestartPolicy)

			// Check if the pod is being deleted
			if pod.DeletionTimestamp != nil {
				t.Logf("Pod %s/%s is being deleted (has deletion timestamp)", e.namespace, name)
				return true
			}

			// Check pod conditions
			t.Logf("Pod conditions:")
			for _, cond := range pod.Status.Conditions {
				t.Logf("  %s: %s, Reason: %s, Message: %s",
					cond.Type, cond.Status, cond.Reason, cond.Message)
			}

			// Check pod status
			if pod.Status.Phase == corev1.PodPending {
				t.Logf("Pod status message: %s", pod.Status.Message)
				t.Logf("Pod status reason: %s", pod.Status.Reason)

				// Check container statuses
				for _, containerStatus := range pod.Status.ContainerStatuses {
					t.Logf("Container %s status:", containerStatus.Name)
					if containerStatus.State.Waiting != nil {
						t.Logf("  Waiting: Reason=%s, Message=%s",
							containerStatus.State.Waiting.Reason,
							containerStatus.State.Waiting.Message)
					}
				}
			}

			// Get events for the pending pod
			events, err := e.GetClient().CoreV1().Events(e.namespace).List(context.Background(), metav1.ListOptions{
				FieldSelector: fmt.Sprintf("involvedObject.name=%s", pod.Name),
			})
			if err != nil {
				t.Logf("Error getting events for %s pod: %v", pod.Name, err)
			} else {
				t.Logf("Events for pod %s:", pod.Name)
				for _, event := range events.Items {
					t.Logf("  %s %s %s: %s",
						event.LastTimestamp, event.Type, event.Reason, event.Message)
				}
			}

			return false // Pod still exists
		}
	})
}

func (e *TestEnv) waitForCronJob(t *testing.T, name string, shouldBeSuspended bool, timeout time.Duration) bool {
	return waitForCondition(timeout, func() bool {
		cronJob, err := e.GetClient().BatchV1().CronJobs(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			t.Logf("CronJob %s/%s not found yet: %v", e.namespace, name, err)
			return false
		}

		if cronJob.Spec.Suspend == nil {
			t.Logf("CronJob %s/%s has nil suspend field", e.namespace, name)
			return false
		}

		isSuspended := *cronJob.Spec.Suspend
		t.Logf("CronJob %s/%s found, suspended: %v, expected suspended: %v",
			e.namespace, name, isSuspended, shouldBeSuspended)
		return isSuspended == shouldBeSuspended
	})
}

func (e *TestEnv) waitForDeployment(t *testing.T, name string, expectedReplicas int32, timeout time.Duration) bool {
	return waitForCondition(timeout, func() bool {
		deployment, err := e.GetClient().AppsV1().Deployments(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			t.Logf("Deployment %s/%s not found yet: %v", e.namespace, name, err)
			return false
		}

		replicas := int32(0)
		if deployment.Spec.Replicas != nil {
			replicas = *deployment.Spec.Replicas
		}

		t.Logf("Deployment %s/%s found, replicas: %d, expected: %d",
			e.namespace, name, replicas, expectedReplicas)
		return replicas == expectedReplicas
	})
}

// setupRedisStorage sets up Redis storage
func (e *TestEnv) setupRedisStorage() {
	if e.mode == MockMode {
		s, err := miniredis.Run()
		require.NoError(e.t, err, "Failed to start miniredis")
		e.redisServer = s
		e.redisClient = redis.NewClient(&redis.Options{
			Addr: s.Addr(),
		})
		e.t.Logf("Connected to mock Redis at %s", s.Addr())
	} else {
		// Use real Redis for integration tests
		redisAddr := os.Getenv("HIGHLANDER_REDIS_ADDR")
		if redisAddr == "" {
			redisAddr = "localhost:6379"
		}
		e.redisClient = redis.NewClient(&redis.Options{
			Addr: redisAddr,
		})
		// Test Redis connection
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := e.redisClient.Ping(ctx).Result()
		require.NoError(e.t, err, "Failed to connect to Redis at %s", redisAddr)
		e.t.Logf("Connected to real Redis at %s", redisAddr)
	}

	// Create Redis storage
	e.leaderStorage = storage.NewRedisStorage(e.redisClient)
}

// setupDBStorage sets up database storage
func (e *TestEnv) setupDBStorage() {
	if e.mode == MockMode {
		// Use in-memory SQLite for testing
		db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		require.NoError(e.t, err, "Failed to create in-memory SQLite database")
		e.dbClient = db
		e.t.Log("Connected to in-memory SQLite database")
	} else {
		// Use real database for integration tests
		dbURL := os.Getenv("HIGHLANDER_DATABASE_URL")
		if dbURL == "" {
			dbURL = "file:test.db?mode=memory&cache=shared"
		}
		db, err := gorm.Open(sqlite.Open(dbURL), &gorm.Config{})
		require.NoError(e.t, err, "Failed to connect to database at %s", dbURL)
		e.dbClient = db
		e.t.Logf("Connected to real database at %s", dbURL)
	}

	// Create DB storage
	dbStorage, err := storage.NewDBStorage(e.dbClient)
	require.NoError(e.t, err, "Failed to create DB storage")
	e.leaderStorage = dbStorage
}

// setupClusters sets up the test clusters
func (e *TestEnv) setupClusters() {
	if e.mode == MockMode {
		// Create mock clusters with different priorities
		e.cluster = common.ClusterConfig{
			Name: "primary",
		}
		e.SetClient(fake.NewSimpleClientset())

		// Add some nodes to the fake clusters
		for j := 0; j < 3; j++ {
			_, err := e.GetClient().CoreV1().Nodes().Create(
				context.Background(),
				&v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("node-%s-%d", e.cluster.Name, j),
					},
				},
				metav1.CreateOptions{},
			)
			require.NoError(e.t, err)
		}
	} else {
		// For real mode, use actual kubeconfig files
		e.cluster = e.initializeRealClusters()
	}
}

// initializeRealClusters initializes connections to real Kubernetes clusters
func (e *TestEnv) initializeRealClusters() (cluster common.ClusterConfig) {
	mock := common.ClusterConfig{
		Name: "mock-primary",
	}
	mock.SetClient(fake.NewSimpleClientset())
	// Check for kubeconfig files in environment variables
	primaryKubeconfig := os.Getenv("PRIMARY_KUBECONFIG")
	secondaryKubeconfig := os.Getenv("SECONDARY_KUBECONFIG")

	// If not set, try to use default kubeconfig
	if primaryKubeconfig == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			primaryKubeconfig = filepath.Join(homeDir, ".kube", "config")
			e.t.Logf("Using default kubeconfig at %s", primaryKubeconfig)
		}
	}

	// Add primary cluster if configured
	if primaryKubeconfig != "" {
		cluster = common.ClusterConfig{
			Name:       "primary",
			Kubeconfig: primaryKubeconfig,
		}
	} else if secondaryKubeconfig != "" {
		cluster = common.ClusterConfig{
			Name:       "secondary",
			Kubeconfig: secondaryKubeconfig,
		}
	}

	// Initialize clients for each cluster
	config, err := clientcmd.BuildConfigFromFlags("", cluster.Kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig for %s cluster: %s", cluster.Name, err.Error())
		return mock
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Error creating kubernetes client for %s cluster: %s", cluster.Name, err.Error())
		return mock
	}

	cluster.SetClient(clientset)

	// Verify connection by listing nodes
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing nodes in %s cluster: %s", cluster.Name, err.Error())
		return mock
	}

	e.t.Logf("Connected to %s cluster with %d nodes", cluster.Name, len(nodes.Items))

	// If no real clusters were configured, fall back to mock clusters
	if cluster.GetClient() == nil {
		klog.Warning("No real clusters configured, falling back to mock clusters")
		return mock
	}

	return cluster
}

// CreateController creates a new leader controller with the given ID
func (e *TestEnv) CreateController(id string, tenant string) *LeaderController {
	// Use the mock workload manager for tests
	var workloadMgr workloads.Manager
	if e.mode == RealMode && os.Getenv("USE_REAL_WORKLOADS") == "true" {
		// Create a real workload manager for integration testing
		workloadMgr = e.createRealWorkloadManager(e.GetClient())
		e.t.Log("Using real workload manager")
	} else {
		// Use the mock workload manager for unit testing
		workloadMgr = e.mockWorkloadMgr
		e.t.Log("Using mock workload manager")
	}

	// Create the controller
	controller := NewLeaderController(
		e.leaderStorage,
		id,
		&e.cluster,
		e.metrics,
		e.monitorServer,
		workloadMgr,
		tenant,
	)

	e.controllers[id] = controller
	return controller
}

func (e *TestEnv) createRealWorkloadManager(kubernetes.Interface) workloads.Manager {
	// Create a real workload manager
	manager := e.NewManager()

	// Add a simple singleton process workload that just echoes messages
	processConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "singleton-echo-process",
			Namespace:              e.namespace,
			Image:                  "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"echo 'Starting singleton process'",
					"echo 'Process ID: $$'",
					"echo 'Hostname: '$(hostname)",
					"echo 'Running on node: '$(cat /etc/nodename 2>/dev/null || echo 'unknown')",
					"i=0",
					"while true; do",
					"  echo \"Singleton process heartbeat: $i at $(date)\"",
					"  i=$((i+1))",
					"  sleep 10",
					"done",
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"SINGLETON": "true",
				"TEST_ENV":  "integration-test",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "100m",
				MemoryRequest: "64Mi",
				CPULimit:      "100m",
				MemoryLimit:   "64Mi",
			},
			RestartPolicy: "Never",
		},
	}

	// Create the process workload
	processWorkload, err := process.NewProcessWorkload(processConfig, e.metrics, e.monitorServer)
	if err != nil {
		e.t.Logf("Failed to create singleton process workload: %v", err)
		return manager
	}

	// Add the workload to the manager
	if err := manager.AddWorkload(processWorkload.GetName(), processWorkload); err != nil {
		e.t.Logf("Failed to add singleton process workload to manager: %v", err)
		return manager
	}

	e.t.Logf("Added singleton process workload to manager")

	// Add a simple cron job that runs every minute
	cronJobConfig := common.CronJobConfig{
		Schedule: "*/1 * * * *", // Every minute
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "singleton-cron-job",
			Namespace:              e.namespace,
			Image:                  "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"echo 'Starting singleton cron job'",
					"echo 'Running at: '$(date)",
					"echo 'Hostname: '$(hostname)",
					"echo 'Completed singleton cron job'",
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"SINGLETON": "true",
				"TEST_ENV":  "integration-test",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "50m",
				MemoryRequest: "32Mi",
				CPULimit:      "100m",
				MemoryLimit:   "64Mi",
			},
			RestartPolicy: "OnFailure",
		},
	}

	// Create the cron job workload
	cronJobWorkload, err := cronjob.NewCronJobWorkload(cronJobConfig, e.metrics, e.monitorServer)
	if err != nil {
		e.t.Logf("Failed to create singleton cron job workload: %v", err)
		return manager
	}

	// Add the workload to the manager
	if err := manager.AddWorkload(cronJobWorkload.GetName(), cronJobWorkload); err != nil {
		e.t.Logf("Failed to add singleton cron job workload to manager: %v", err)
		return manager
	}

	e.t.Logf("Added singleton cron job workload to manager")

	return manager
}

func (e *TestEnv) cleanupStorageKeys() {
	if e.storageType == RedisStorage && e.redisClient != nil {
		ctx := context.Background()

		// Find and delete all test-related keys
		keys, err := e.redisClient.Keys(ctx, "highlander-leader-*").Result()
		if err != nil {
			e.t.Logf("Error listing Redis keys: %v", err)
			return
		}

		if len(keys) > 0 {
			e.t.Logf("Cleaning up %d Redis keys", len(keys))
			for _, key := range keys {
				e.t.Logf("Deleting Redis key: %s", key)
				e.redisClient.Del(ctx, key)
			}
		}
	} else if e.storageType == DBStorage && e.dbClient != nil {
		// Clean up database tables
		e.dbClient.Exec("DELETE FROM leader_locks")
	}
}

func (e *TestEnv) LogPods() {
	if e.mode != RealMode {
		return
	}

	pods, err := e.GetClient().CoreV1().Pods(e.namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing pods: %v", err)
		return
	}

	e.t.Logf("Found %d pods in namespace %s:", len(pods.Items), e.namespace)
	for _, pod := range pods.Items {
		e.t.Logf("  Pod: %s, Status: %s, Node: %s",
			pod.Name, pod.Status.Phase, pod.Spec.NodeName)

		// Log container statuses
		for _, containerStatus := range pod.Status.ContainerStatuses {
			e.t.Logf("    Container: %s, Ready: %v, RestartCount: %d",
				containerStatus.Name, containerStatus.Ready, containerStatus.RestartCount)

			if containerStatus.State.Waiting != nil {
				e.t.Logf("    Waiting: Reason=%s, Message=%s",
					containerStatus.State.Waiting.Reason,
					containerStatus.State.Waiting.Message)
			}

			if containerStatus.State.Terminated != nil {
				e.t.Logf("    Terminated: Reason=%s, Message=%s, ExitCode=%d",
					containerStatus.State.Terminated.Reason,
					containerStatus.State.Terminated.Message,
					containerStatus.State.Terminated.ExitCode)
			}
		}
	}
	// Get node information
	nodes, err := e.GetClient().CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing nodes: %v", err)
	} else {
		e.t.Logf("Cluster has %d nodes:", len(nodes.Items))
		for _, node := range nodes.Items {
			e.t.Logf("  Node: %s", node.Name)
			e.t.Logf("    Allocatable: %v", node.Status.Allocatable)
			//e.t.Logf("    Capacity: %v", node.Status.Capacity)
			//e.t.Logf("    Conditions: %v", node.Status.Conditions)
		}
	}
	autoscalerEvents, err := e.GetClient().CoreV1().Events("kube-system").List(context.Background(), metav1.ListOptions{
		FieldSelector: "source=cluster-autoscaler",
	})
	if err == nil {
		e.t.Logf("Cluster autoscaler events:")
		for _, event := range autoscalerEvents.Items {
			e.t.Logf("  %s %s: %s", event.Reason, event.Type, event.Message)
		}
	}
	quotas, err := e.GetClient().CoreV1().ResourceQuotas(e.namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing resource quotas: %v", err)
	} else {
		e.t.Logf("Found %d resource quotas:", len(quotas.Items))
		for _, quota := range quotas.Items {
			e.t.Logf("  Quota: %s", quota.Name)
			e.t.Logf("    Hard limits: %v", quota.Spec.Hard)
			e.t.Logf("    Used: %v", quota.Status.Used)
		}
	}
}

// Cleanup cleans up the test environment
func (e *TestEnv) NewManager() workloads.Manager {
	return workloads.NewManager(e.metrics, e.monitorServer)
}

// Cleanup cleans up the test environment
func (e *TestEnv) Cleanup() {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Stop all controllers
	for id, controller := range e.controllers {
		e.t.Logf("Stopping controller %s", id)
		if err := controller.Stop(cleanupCtx); err != nil {
			e.t.Logf("Error stopping controller %s: %v", id, err)
		}
		delete(e.controllers, id)
	}

	// Clean up storage keys
	e.cleanupStorageKeys()

	// Close storage connections
	if e.leaderStorage != nil {
		_ = e.leaderStorage.Close()
		e.leaderStorage = nil
	}

	// Close Redis connections
	if e.redisClient != nil {
		_ = e.redisClient.Close()
		e.redisClient = nil
	}

	// Stop miniredis if using mock mode
	if e.mode == MockMode && e.redisServer != nil {
		e.redisServer.Close()
		e.redisServer = nil
	}

	if e.mode == RealMode {
		e.ForceCleanupNamespaceResources()

		e.t.Logf("Deleting namespace %s", e.namespace)

		// First delete all pods in the namespace to speed up cleanup
		err := e.GetClient().CoreV1().Pods(e.namespace).DeleteCollection(
			cleanupCtx,
			metav1.DeleteOptions{},
			metav1.ListOptions{},
		)
		if err != nil && !errors.IsNotFound(err) {
			e.t.Logf("Error deleting pods in namespace %s: %v", e.namespace, err)
		}
		if strings.Contains(e.namespace, "test") {
			err = e.GetClient().CoreV1().Namespaces().Delete(
				context.Background(),
				e.namespace,
				metav1.DeleteOptions{},
			)
			if err != nil && !errors.IsNotFound(err) {
				e.t.Logf("Failed to delete namespace %s: %v", e.namespace, err)
			} else {
				e.t.Logf("Deleted namespace: %s", e.namespace)
			}
		}
	}
}

// Helper function to get the current value of a Prometheus gauge
func getGaugeValue(gauge prometheus.Gauge) float64 {
	return testutil.ToFloat64(gauge)
}

// Helper function to get the current value of a Prometheus counter
func getCounterValue(counter prometheus.Counter) float64 {
	return testutil.ToFloat64(counter)
}

func (e *TestEnv) ForceCleanupNamespaceResources() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	e.t.Logf("Force cleaning up all resources in namespace %s", e.namespace)

	// Delete all pods with force
	pods, err := e.GetClient().CoreV1().Pods(e.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing pods for cleanup: %v", err)
	} else {
		e.t.Logf("Found %d pods to clean up", len(pods.Items))
		for _, pod := range pods.Items {
			e.t.Logf("Force deleting pod: %s", pod.Name)

			// Force delete with 0 grace period
			gracePeriod := int64(0)
			background := metav1.DeletePropagationBackground
			err := e.GetClient().CoreV1().Pods(e.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
				PropagationPolicy:  &background,
			})

			if err != nil && !errors.IsNotFound(err) {
				e.t.Logf("Error force deleting pod %s: %v", pod.Name, err)
			}
		}
	}

	// Delete all deployments
	deployments, err := e.GetClient().AppsV1().Deployments(e.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing deployments for cleanup: %v", err)
	} else {
		e.t.Logf("Found %d deployments to clean up", len(deployments.Items))
		for _, deployment := range deployments.Items {
			e.t.Logf("Deleting deployment: %s", deployment.Name)
			err := e.GetClient().AppsV1().Deployments(e.namespace).Delete(ctx, deployment.Name, metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				e.t.Logf("Error deleting deployment %s: %v", deployment.Name, err)
			}
		}
	}

	// Delete all statefulsets
	statefulsets, err := e.GetClient().AppsV1().StatefulSets(e.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing statefulsets for cleanup: %v", err)
	} else {
		e.t.Logf("Found %d statefulsets to clean up", len(statefulsets.Items))
		for _, statefulset := range statefulsets.Items {
			e.t.Logf("Deleting statefulset: %s", statefulset.Name)
			err := e.GetClient().AppsV1().StatefulSets(e.namespace).Delete(ctx, statefulset.Name, metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				e.t.Logf("Error deleting statefulset %s: %v", statefulset.Name, err)
			}
		}
	}

	// Delete all services
	services, err := e.GetClient().CoreV1().Services(e.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing services for cleanup: %v", err)
	} else {
		e.t.Logf("Found %d services to clean up", len(services.Items))
		for _, item := range services.Items {
			// Skip the kubernetes item
			if item.Name == "kubernetes" {
				continue
			}
			e.t.Logf("Deleting item: %s", item.Name)
			err := e.GetClient().CoreV1().Services(e.namespace).Delete(ctx, item.Name, metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				e.t.Logf("Error deleting item %s: %v", item.Name, err)
			}
		}
	}

	// Delete all cronjobs
	cronjobs, err := e.GetClient().BatchV1().CronJobs(e.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing cronjobs for cleanup: %v", err)
	} else {
		e.t.Logf("Found %d cronjobs to clean up", len(cronjobs.Items))
		for _, item := range cronjobs.Items {
			e.t.Logf("Deleting item: %s", item.Name)
			err := e.GetClient().BatchV1().CronJobs(e.namespace).Delete(ctx, item.Name, metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				e.t.Logf("Error deleting item %s: %v", item.Name, err)
			}
		}
	}

	// Delete all jobs
	jobs, err := e.GetClient().BatchV1().Jobs(e.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		e.t.Logf("Error listing jobs for cleanup: %v", err)
	} else {
		e.t.Logf("Found %d jobs to clean up", len(jobs.Items))
		for _, job := range jobs.Items {
			e.t.Logf("Deleting job: %s", job.Name)
			err := e.GetClient().BatchV1().Jobs(e.namespace).Delete(ctx, job.Name, metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				e.t.Logf("Error deleting job %s: %v", job.Name, err)
			}
		}
	}

	err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			pods, err := e.GetClient().CoreV1().Pods(e.namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				e.t.Logf("Error checking for remaining pods: %v", err)
				return false, nil
			}

			if len(pods.Items) == 0 {
				return true, nil
			}

			e.t.Logf("%d pods still exist in namespace %s", len(pods.Items), e.namespace)
			for _, pod := range pods.Items {
				e.t.Logf("  Pod %s: phase=%s, deletionTimestamp=%v",
					pod.Name, pod.Status.Phase, pod.DeletionTimestamp)

				// If pod has been stuck in Terminating for too long, try to remove finalizers
				if pod.DeletionTimestamp != nil && len(pod.Finalizers) > 0 {
					terminatingTime := time.Since(pod.DeletionTimestamp.Time)
					if terminatingTime > 5*time.Second {
						e.t.Logf("  Pod %s stuck terminating for %v, removing finalizers", pod.Name, terminatingTime)

						// Create a patch to remove finalizers
						patchBytes, _ := json.Marshal(map[string]interface{}{
							"metadata": map[string]interface{}{
								"finalizers": nil,
							},
						})

						_, patchErr := e.GetClient().CoreV1().Pods(e.namespace).Patch(
							ctx, pod.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
						if patchErr != nil {
							e.t.Logf("    Error removing finalizers: %v", patchErr)
						}
					}
				}
			}

			return false, nil
		})

	if err != nil {
		e.t.Logf("Timed out waiting for pods to be deleted: %v", err)
	}

	e.t.Logf("Finished force cleanup of namespace %s", e.namespace)
}

// Helper function to wait for a condition with timeout
func waitForCondition(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// Tests start here

func TestLeaderElection(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()
	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
	defer cancel()

	//go func() {
	//	select {
	//	case <-ctx.Done():
	//		if ctx.Err() == context.DeadlineExceeded {
	//			fmt.Println("=== GOROUTINE DUMP ON TEST TIMEOUT ===")
	//			pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
	//			fmt.Println("=== END GOROUTINE DUMP ===")
	//		}
	//	}
	//}()

	// Create controllers
	controller1 := env.CreateController("controller-1", "tenant")
	controller2 := env.CreateController("controller-2", "tenant")

	// Start both controllers
	err := controller1.Start(ctx)
	require.NoError(t, err)

	err = controller2.Start(ctx)
	require.NoError(t, err)

	// Wait for leadership to stabilize
	time.Sleep(2 * time.Second)

	// Verify controller1 is the leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller1.IsLeader()
	})
	require.True(t, success, "Controller 1 should be the leader")
	require.False(t, controller2.IsLeader(), "Controller 2 should not be the leader")

	// Verify workloads were started on controller1
	if env.mode == MockMode {
		require.Equal(t, 1, env.mockWorkloadMgr.GetStartCalls(), "Workloads should be started on controller 1")
	}

	// Stop the first controller to simulate failure
	t.Logf("Stopping controller 1 to simulate failure %s", common.GetElapsedTime(ctx))
	err = controller1.Stop(ctx)
	require.NoError(t, err)

	// Wait for leadership to transfer to controller2
	success = waitForCondition(15*time.Second, func() bool {
		return controller2.IsLeader()
	})
	require.True(t, success, "Leadership should transfer to controller 2")

	// Wait for background work to start
	time.Sleep(5 * time.Second)

	// Verify workloads were stopped on controller1 and started on controller2
	if env.mode == MockMode {
		require.Equal(t, 2, env.mockWorkloadMgr.GetStartCalls(), "Workloads should be started on controller 2: %v [elapsed %s]",
			controller2.IsLeader(), common.GetElapsedTime(ctx))
		require.Equal(t, 1, env.mockWorkloadMgr.GetStopCalls(), "Workloads should be stopped on controller 1: %v [elapsed %s]",
			controller2.IsLeader(), common.GetElapsedTime(ctx))
	}

	// Stop the second controller
	err = controller2.Stop(ctx)
	require.NoError(t, err)

	// Verify workloads were stopped on controller2
	if env.mode == MockMode {
		require.Equal(t, 2, env.mockWorkloadMgr.GetStopCalls(), "Workloads should be stopped on controller 2")
	}
}

func TestMultiTenantLeadership(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
	defer cancel()

	// Create controllers for different tenants
	controller1A := env.CreateController("controller-1A", "tenant-a")
	controller2A := env.CreateController("controller-2A", "tenant-a")
	controller1B := env.CreateController("controller-1B", "tenant-b")
	controller2B := env.CreateController("controller-2B", "tenant-b")

	// Start all controllers
	for _, c := range []*LeaderController{controller1A, controller2A, controller1B, controller2B} {
		err := c.Start(ctx)
		require.NoError(t, err)
	}

	// Wait for leadership to stabilize
	time.Sleep(2 * time.Second)

	// Verify one leader per tenant
	success1A := waitForCondition(5*time.Second, func() bool {
		return controller1A.IsLeader()
	})
	require.True(t, success1A, "Controller 1A should be the leader for tenant-a")
	require.False(t, controller2A.IsLeader(), "Controller 2A should not be the leader for tenant-a")

	success1B := waitForCondition(5*time.Second, func() bool {
		return controller1B.IsLeader()
	})
	require.True(t, success1B, "Controller 1B should be the leader for tenant-b")
	require.False(t, controller2B.IsLeader(), "Controller 2B should not be the leader for tenant-b")
}

func TestWorkloadFailover(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	// Clean up storage keys before starting
	env.cleanupStorageKeys()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
	defer cancel()

	// Create controllers
	controller1 := env.CreateController("controller-1", "tenant")
	controller2 := env.CreateController("controller-2", "tenant")

	// Start both controllers
	err := controller1.Start(ctx)
	require.NoError(t, err)

	err = controller2.Start(ctx)
	require.NoError(t, err)

	// Wait for controller1 to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller1.IsLeader()
	})
	require.True(t, success, "Controller 1 should become leader")

	// In real mode, we might not be able to track start calls with the mock
	if env.mode == MockMode {
		require.Equal(t, 1, env.mockWorkloadMgr.GetStartCalls(), "Workloads should be started on controller 1")
	}

	// Stop the first controller to simulate failure
	t.Log("Stopping controller 1 to simulate failure")
	err = controller1.Stop(ctx)
	require.NoError(t, err)

	// Wait for workloads to be stopped
	success = waitForCondition(5*time.Second, func() bool {
		return env.mockWorkloadMgr.GetStopCalls() == 1
	})
	require.True(t, success, "Workloads should be stopped on controller 1")

	// Wait for leadership to transfer to controller2
	success = waitForCondition(15*time.Second, func() bool {
		return controller2.IsLeader()
	})
	require.True(t, success, "Leadership should transfer to controller 2")

	// Wait for background work to start
	time.Sleep(3 * time.Second)

	if env.mode == MockMode {
		require.Equal(t, 2, env.mockWorkloadMgr.GetStartCalls(), "Workloads should be started on controller 2")
		require.Equal(t, 1, env.mockWorkloadMgr.GetStopCalls(), "Workloads should be stopped on controller 1")
	}
}

func TestWorkloadErrorHandling(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
	defer cancel()

	// Set up workload manager to return errors
	env.mockWorkloadMgr.SetStartError(fmt.Errorf("simulated start error"))

	// Create and start a controller
	controller := env.CreateController("controller-1", "tenant")
	err := controller.Start(ctx)
	require.NoError(t, err)

	// Wait for the controller to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader despite workload error")

	time.Sleep(100 * time.Millisecond)
	// Verify workload start was attempted
	require.Equal(t, 1, env.mockWorkloadMgr.GetStartCalls(), "Workload start should be attempted")

	// Now set up stop error and stop the controller
	env.mockWorkloadMgr.SetStopError(fmt.Errorf("simulated stop error"))
	err = controller.Stop(ctx)
	require.NoError(t, err, "Controller should stop despite workload error")

	// Verify workload stop was attempted
	require.Equal(t, 1, env.mockWorkloadMgr.GetStopCalls(), "Workload stop should be attempted")
}

func TestMetricsReporting(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
	defer cancel()

	// Create and start a controller
	controller := env.CreateController("controller-metrics", "tenant")
	err := controller.Start(ctx)
	require.NoError(t, err)

	// Wait for the controller to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// Verify metrics
	require.Equal(t, float64(1), getGaugeValue(env.metrics.IsLeader), "IsLeader metric should be 1")
	require.True(t, getCounterValue(env.metrics.LeadershipAcquisitions) > 0, "LeadershipAcquisitions should be incremented")

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)

	// Verify metrics after stopping
	require.Equal(t, float64(0), getGaugeValue(env.metrics.IsLeader), "IsLeader metric should be 0")
	require.True(t, getCounterValue(env.metrics.LeadershipLosses) > 0, "LeadershipLosses should be incremented")
}

func TestStorageImplementations(t *testing.T) {
	// Test with Redis storage
	t.Run("Redis", func(t *testing.T) {
		_ = os.Setenv("HIGHLANDER_STORAGE_TYPE", "redis")
		defer os.Unsetenv("HIGHLANDER_STORAGE_TYPE")

		env := NewTestEnv(t, "")
		defer env.Cleanup()

		ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
		defer cancel()

		// Create controllers
		controller1 := env.CreateController("redis-controller-1", "tenant")
		controller2 := env.CreateController("redis-controller-2", "tenant")

		// Start both controllers
		err := controller1.Start(ctx)
		require.NoError(t, err)

		err = controller2.Start(ctx)
		require.NoError(t, err)

		// Wait for leadership to stabilize
		time.Sleep(2 * time.Second)

		// Verify controller1 is the leader
		success := waitForCondition(5*time.Second, func() bool {
			return controller1.IsLeader()
		})
		require.True(t, success, "Controller 1 should be the leader with Redis storage")
		require.False(t, controller2.IsLeader(), "Controller 2 should not be the leader with Redis storage")

		// Stop controllers
		err = controller1.Stop(ctx)
		require.NoError(t, err)

		err = controller2.Stop(ctx)
		require.NoError(t, err)
	})

	// Test with DB storage
	t.Run("DB", func(t *testing.T) {
		_ = os.Setenv("HIGHLANDER_STORAGE_TYPE", "db")
		defer os.Unsetenv("HIGHLANDER_STORAGE_TYPE")

		env := NewTestEnv(t, "")
		defer env.Cleanup()

		ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
		defer cancel()

		// Create controllers
		controller1 := env.CreateController("db-controller-1", "tenant")
		controller2 := env.CreateController("db-controller-2", "tenant")

		// Start both controllers
		err := controller1.Start(ctx)
		require.NoError(t, err)

		err = controller2.Start(ctx)
		require.NoError(t, err)

		// Wait for leadership to stabilize
		time.Sleep(2 * time.Second)

		// Verify controller1 is the leader
		success := waitForCondition(5*time.Second, func() bool {
			return controller1.IsLeader()
		})
		require.True(t, success, "Controller 1 should be the leader with DB storage")
		require.False(t, controller2.IsLeader(), "Controller 2 should not be the leader with DB storage")

		// Stop controllers
		err = controller1.Stop(ctx)
		require.NoError(t, err)

		err = controller2.Stop(ctx)
		require.NoError(t, err)
	})
}

func TestLeaderFailoverWithWorkloads(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 35*time.Second)
	defer cancel()

	// Create workload managers for each controller
	workloadMgr1 := NewMockWorkloadManager()
	workloadMgr2 := NewMockWorkloadManager()

	// Add a simple process workload to both managers
	processWorkload1 := NewMockWorkload("failover-test-process", common.WorkloadTypeProcess)
	err := workloadMgr1.AddWorkload(processWorkload1.GetName(), processWorkload1)
	require.NoError(t, err)

	processWorkload2 := NewMockWorkload("failover-test-process", common.WorkloadTypeProcess)
	err = workloadMgr2.AddWorkload(processWorkload2.GetName(), processWorkload2)
	require.NoError(t, err)

	// Create controllers
	controller1 := NewLeaderController(
		env.leaderStorage,
		"failover-controller-1",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr1,
		"failover-test",
	)

	controller2 := NewLeaderController(
		env.leaderStorage,
		"failover-controller-2",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr2,
		"failover-test",
	)

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

	// Wait for the process to start on controller 1
	time.Sleep(2 * time.Second)

	// Verify process was started on controller 1
	require.True(t, processWorkload1.IsStarted(), "Process should be started on controller 1")
	require.False(t, processWorkload2.IsStarted(), "Process should not be started on controller 2")

	// Stop controller 1 to simulate failure
	t.Log("Stopping controller 1 to simulate failure")
	err = controller1.Stop(ctx)
	require.NoError(t, err)

	// Wait for controller 2 to become leader
	success = waitForCondition(20*time.Second, func() bool {
		return controller2.IsLeader()
	})
	require.True(t, success, "Controller 2 should become leader after controller 1 failure")

	// Wait for the process to start on controller 2
	time.Sleep(2 * time.Second)

	// Verify process was stopped on controller 1 and started on controller 2
	require.True(t, processWorkload1.IsStopped(), "Process should be stopped on controller 1")
	require.True(t, processWorkload2.IsStarted(), "Process should be started on controller 2")

	// Stop controller 2
	err = controller2.Stop(ctx)
	require.NoError(t, err)

	// Wait for the process to stop on controller 2
	time.Sleep(2 * time.Second)

	// Verify process was stopped on controller 2
	require.True(t, processWorkload2.IsStopped(), "Process should be stopped on controller 2")
}

func TestConcurrentLeaderElection(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
	defer cancel()

	// Create multiple controllers
	numControllers := 5
	controllers := make([]*LeaderController, numControllers)

	for i := 0; i < numControllers; i++ {
		controllers[i] = env.CreateController(fmt.Sprintf("concurrent-controller-%d", i), "tenant")
	}

	// Start all controllers concurrently
	var wg sync.WaitGroup
	for i := 0; i < numControllers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := controllers[idx].Start(ctx)
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// Wait for leadership to stabilize
	time.Sleep(3 * time.Second)

	// Verify only one controller is the leader
	leaderCount := 0
	leaderIdx := -1
	for i, c := range controllers {
		if c.IsLeader() {
			leaderCount++
			leaderIdx = i
		}
	}

	require.Equal(t, 1, leaderCount, "Only one controller should be the leader")
	require.True(t, leaderIdx >= 0, "One controller should be the leader")

	t.Logf("Controller %d is the leader", leaderIdx)

	// Stop the leader
	err := controllers[leaderIdx].Stop(ctx)
	require.NoError(t, err)

	// Wait for leadership to transfer
	time.Sleep(10 * time.Second)

	// Verify a new leader was elected
	newLeaderCount := 0
	newLeaderIdx := -1
	for i, c := range controllers {
		if i != leaderIdx && c.IsLeader() {
			newLeaderCount++
			newLeaderIdx = i
		}
	}

	require.Equal(t, 1, newLeaderCount, "Only one new controller should be the leader")
	require.True(t, newLeaderIdx >= 0 && newLeaderIdx != leaderIdx, "A different controller should be the new leader")

	t.Logf("Controller %d is the new leader", newLeaderIdx)

	// Stop all remaining controllers
	for i, c := range controllers {
		if i != leaderIdx {
			err := c.Stop(ctx)
			require.NoError(t, err)
		}
	}
}

func TestLeaderLockExpiration(t *testing.T) {
	// This test specifically checks if the lock expires properly
	// and can be acquired by another controller

	env := NewTestEnv(t, "")
	defer env.Cleanup()

	// Use a longer timeout for integration testing
	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 2*time.Minute)
	defer cancel()

	// Create controllers
	controller1 := env.CreateController("expiration-controller-1", "tenant")
	controller2 := env.CreateController("expiration-controller-2", "tenant")

	// Start the first controller
	err := controller1.Start(ctx)
	require.NoError(t, err)

	// Wait for controller1 to become leader
	success := waitForCondition(10*time.Second, func() bool {
		return controller1.IsLeader()
	})
	require.True(t, success, "Controller 1 should become leader")

	// Log the current lock status
	if env.storageType == RedisStorage && env.redisClient != nil {
		lockKey := fmt.Sprintf("highlander-leader-lock:%s", "tenant")
		val, err := env.redisClient.Get(ctx, lockKey).Result()
		if err != nil {
			t.Logf("Error getting lock value: %v", err)
		} else {
			t.Logf("Current lock value: %s", val)
		}

		ttl, err := env.redisClient.TTL(ctx, lockKey).Result()
		if err != nil {
			t.Logf("Error getting lock TTL: %v", err)
		} else {
			t.Logf("Current lock TTL: %v", ttl)
		}
	}

	// Instead of stopping controller1 properly, we'll simulate a crash
	// by directly closing the stopCh without releasing the lock
	t.Log("Simulating crash of controller 1 by closing stopCh")
	close(controller1.stopCh)

	// Start the second controller
	t.Log("Starting controller 2")
	err = controller2.Start(ctx)
	require.NoError(t, err)

	// If we're using miniredis in mock mode, we need to manually expire the key
	// This is acceptable because miniredis doesn't automatically expire keys
	if env.mode == MockMode {
		lockKey := fmt.Sprintf("highlander-leader-lock:%s", "tenant")

		if env.redisServer != nil {
			// Fast forward time in miniredis to expire the key
			t.Logf("Fast forwarding time in miniredis to expire the lock")
			env.redisServer.FastForward(LockTTL + 1*time.Second)
		} else if env.storageType == DBStorage && env.dbClient != nil {
			// For DB storage in mock mode, we need to delete the record
			t.Logf("Manually expiring the lock in the mock database")
			env.dbClient.Exec("DELETE FROM leader_locks WHERE key = ?", lockKey)
		}
	}

	// Wait for the lock to expire and controller2 to become leader
	// In real mode, we'll wait for the natural TTL expiration
	t.Log("Waiting for controller 2 to become leader after lock expiration")

	// Calculate how long we should wait based on the TTL
	waitTime := LockTTL * 2 // Wait for twice the TTL to be safe

	success = waitForCondition(waitTime, func() bool {
		isLeader := controller2.IsLeader()

		// Log the current status but don't modify anything
		if !isLeader && env.mode == RealMode && env.storageType == RedisStorage && env.redisClient != nil {
			lockKey := fmt.Sprintf("highlander-leader-lock:%s", "tenant")
			val, err := env.redisClient.Get(ctx, lockKey).Result()
			if err != nil && !ierrors.Is(err, redis.Nil) {
				t.Logf("Error getting lock value: %v", err)
			} else if ierrors.Is(err, redis.Nil) {
				t.Logf("Lock key doesn't exist (expired naturally)")
			} else {
				ttl, _ := env.redisClient.TTL(ctx, lockKey).Result()
				t.Logf("Current lock value: %s, TTL: %v", val, ttl)
			}
		}

		return isLeader
	})

	// We'll require but with a detailed message if it fails
	if !success {
		if env.mode == RealMode {
			t.Logf("FAILURE: Controller 2 did not become leader after waiting %v", waitTime)
			t.Logf("This could be because the lock TTL (%v) is not expiring as expected", LockTTL)
			t.Logf("Check your Redis configuration to ensure keys are being expired properly")

			// Log the final lock status
			if env.storageType == RedisStorage && env.redisClient != nil {
				lockKey := fmt.Sprintf("highlander-leader-lock:%s", "tenant")
				val, err := env.redisClient.Get(ctx, lockKey).Result()
				if err != nil && !ierrors.Is(err, redis.Nil) {
					t.Logf("Final lock status - Error: %v", err)
				} else if ierrors.Is(err, redis.Nil) {
					t.Logf("Final lock status - Key doesn't exist (expired)")
				} else {
					ttl, _ := env.redisClient.TTL(ctx, lockKey).Result()
					t.Logf("Final lock status - Value: %s, TTL: %v", val, ttl)
				}
			}
		}
	}

	require.True(t, success, "Controller 2 should become leader after lock expiration (TTL: %v)", LockTTL)

	// Stop controller2
	t.Log("Stopping controller 2")
	err = controller2.Stop(ctx)
	require.NoError(t, err)
}

func TestClusterHealthCheck(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 15*time.Second)
	defer cancel()

	// Create a controller
	controller := env.CreateController("health-check-controller", "tenant")

	// Start the controller
	err := controller.Start(ctx)
	require.NoError(t, err)

	// Wait for controller to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// Verify cluster is healthy
	isHealthy := controller.IsClusterHealthy(ctx, env.cluster)
	require.True(t, isHealthy, "Cluster should be healthy")

	// Manually trigger a cluster health check
	controller.TriggerClusterHealthCheck(ctx)

	// Controller should still be leader since cluster is healthy
	require.True(t, controller.IsLeader(), "Controller should still be leader after health check")

	// Now simulate an unhealthy cluster by replacing the client with a broken one
	originalClient := env.GetClient()
	env.SetClient(nil)

	// Manually trigger a cluster health check
	controller.TriggerClusterHealthCheck(ctx)

	// Wait for controller to release leadership due to unhealthy cluster
	success = waitForCondition(5*time.Second, func() bool {
		return !controller.IsLeader()
	})
	require.True(t, success, "Controller should release leadership due to unhealthy cluster")

	// Restore the original client
	env.SetClient(originalClient)

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)
}

// Integration tests for real workloads
// These tests are more complex and require a real Kubernetes cluster
// They should be run with HIGHLANDER_TEST_MODE=real
func TestRealWorkloads(t *testing.T) {
	if os.Getenv("HIGHLANDER_TEST_MODE") != "real" {
		t.Skip("Skipping integration test in mock mode")
	}

	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 120*time.Second)
	defer cancel()

	// Create a real workload manager
	workloadMgr := env.NewManager()

	// Add a simple process workload
	processConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "test-process",
			Namespace:              env.namespace, // Use test namespace
			Image:                  "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"echo 'Starting test process'",
					"echo 'Process ID: $$'",
					"sleep 300", // Sleep for 5 minutes
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"TEST_ENV": "test-value",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "100m",
				MemoryRequest: "64Mi",
				CPULimit:      "100m",
				MemoryLimit:   "64Mi",
			},
			RestartPolicy: "Never",
		},
	}

	// Create a process workload
	processWorkload, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer)
	require.NoError(t, err)
	err = workloadMgr.AddWorkload(processWorkload.GetName(), processWorkload)
	require.NoError(t, err)

	// Create a controller with our workload manager
	controller := NewLeaderController(
		env.leaderStorage,
		"real-workload-controller",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"tenant",
	)

	// Start the controller
	err = controller.Start(ctx)
	require.NoError(t, err)

	// Wait for the controller to become leader
	success := waitForCondition(15*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// Log initial state
	t.Log("Controller started and became leader, waiting for pod to start")
	env.LogPods()

	// Wait for the process to start and reach Running state
	success = env.waitForPodState(t, "test-process-pod", true, 19*time.Second)
	require.True(t, success, "Process pod should be running")

	// If the pod is running, get it and log details
	if success {
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "test-process-pod", metav1.GetOptions{})
		if err == nil {
			t.Logf("Pod is running on node: %s", pod.Spec.NodeName)

			// Get pod logs
			logs, err := env.GetClient().CoreV1().Pods(env.namespace).GetLogs("test-process-pod", &corev1.PodLogOptions{}).Do(ctx).Raw()
			if err == nil {
				t.Logf("Pod logs:\n%s", string(logs))
			} else {
				t.Logf("Error getting pod logs: %v", err)
			}
		}
	} else {
		// Log pods again if we couldn't find our pod
		t.Log("Failed to find running pod, current pods:")
		env.LogPods()
	}

	// Stop the controller
	t.Log("Stopping controller")
	err = controller.Stop(ctx)
	require.NoError(t, err)

	// Wait for the process to stop
	t.Log("Waiting for pod to be deleted")
	success = env.waitForPodState(t, "test-process-pod", false, 19*time.Second)
	require.True(t, success, "Process pod should be deleted")

	if !success {
		// Log pods again if the pod wasn't deleted
		t.Log("Pod not deleted, current pods:")
		env.LogPods()
	}
}

// TestAddingDifferentWorkloadTypes tests adding and managing different types of workloads
func TestAddingDifferentWorkloadTypes(t *testing.T) {
	// Skip in mock mode
	if os.Getenv("HIGHLANDER_TEST_MODE") != "real" {
		t.Skip("Skipping integration test in mock mode")
	}

	env := NewTestEnv(t, "")
	defer env.Cleanup()
	t.Cleanup(func() {
		env.Cleanup()
	})

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 3*time.Minute)
	defer cancel()

	// Create a real workload manager instead of the mock
	workloadMgr := env.NewManager()

	// Use the test namespace instead of "default"
	t.Logf("Using namespace: %s", env.namespace)

	// Create and add different types of workloads
	// 1. Process workload
	processConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "test-echo-process",
			Namespace:              env.namespace, // Use test namespace
			Image:                  "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"echo 'Starting test process'",
					"echo 'Process ID: $$'",
					"sleep 300", // Sleep for 5 minutes
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"TEST_ENV": "test-value",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "100m",
				MemoryRequest: "64Mi",
				CPULimit:      "100m",
				MemoryLimit:   "64Mi",
			},
			RestartPolicy: "Never",
		},
	}
	processWorkload, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer)
	require.NoError(t, err)
	err = workloadMgr.AddWorkload(processWorkload.GetName(), processWorkload)
	require.NoError(t, err)

	// 2. CronJob workload
	cronJobConfig := common.CronJobConfig{
		Schedule: "*/5 * * * *", // Every 5 minutes
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "test-echo-cronjob",
			Namespace:              env.namespace, // Use test namespace
			Image:                  "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"echo 'CronJob executed at: '$(date)",
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"TEST_ENV": "test-value",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "50m",
				MemoryRequest: "32Mi",
				CPULimit:      "100m",
				MemoryLimit:   "64Mi",
			},
			RestartPolicy: "OnFailure",
		},
	}
	cronJobWorkload, err := cronjob.NewCronJobWorkload(cronJobConfig, env.metrics, env.monitorServer)
	require.NoError(t, err)
	err = workloadMgr.AddWorkload(cronJobWorkload.GetName(), cronJobWorkload)
	require.NoError(t, err)

	// 3. Deployment workload (like Jobs)
	deploymentConfig := common.ServiceConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "test-echo-deployment",
			Namespace:              env.namespace, // Use test namespace
			Replicas:               1,
			Image:                  "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"while true; do echo 'Deployment running at: '$(date); sleep 60; done",
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"TEST_ENV": "test-value",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "100m",
				MemoryRequest: "64Mi",
				CPULimit:      "100m",
				MemoryLimit:   "64Mi",
			},
		},
	}
	deploymentWorkload, err := service.NewServiceWorkload(deploymentConfig, env.metrics, env.monitorServer)
	require.NoError(t, err)
	err = workloadMgr.AddWorkload(deploymentWorkload.GetName(), deploymentWorkload)
	require.NoError(t, err)

	// Create a controller with our workload manager
	controller := NewLeaderController(
		env.leaderStorage,
		"controller-with-workloads",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"tenant",
	)

	// Start the controller
	err = controller.Start(ctx)
	require.NoError(t, err)

	// Wait for the controller to become leader
	success := waitForCondition(15*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// Wait for workloads to start
	t.Log("Waiting for workloads to start...")

	// Helper function to wait for cronjob to exist and not be suspended
	waitForCronJob := func(name string, timeout time.Duration) bool {
		return waitForCondition(timeout, func() bool {
			cronJob, err := env.GetClient().BatchV1().CronJobs(env.namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				t.Logf("CronJob %s not found yet: %v", name, err)
				return false
			}
			if cronJob.Spec.Suspend == nil {
				t.Logf("CronJob %s has nil suspend field", name)
				return false
			}
			isSuspended := *cronJob.Spec.Suspend
			t.Logf("CronJob %s suspended: %v", name, isSuspended)
			return !isSuspended
		})
	}

	// Wait for process pod to be running
	success = env.waitForPodState(t, "test-echo-process-pod", true, 19*time.Second)
	require.True(t, success, "Process pod should be running")

	// Wait for cronjob to exist and not be suspended
	cronJobSuccess := waitForCronJob("test-echo-cronjob", 17*time.Second)
	require.True(t, cronJobSuccess, "CronJob should exist and not be suspended")

	// Wait for deployment to have 1 replica
	deploymentSuccess := env.waitForDeployment(t, "test-echo-deployment", 1, 17*time.Second)
	require.True(t, deploymentSuccess, "Deployment should have 1 replica")

	// Verify workloads were started
	statuses := workloadMgr.GetAllStatuses()
	require.Equal(t, 3, len(statuses), "Should have 3 workload statuses")

	// Check process workload status
	processStatus, exists := statuses["test-echo-process"]
	require.True(t, exists, "Should have process workload")
	require.Equal(t, common.WorkloadTypeProcess, processStatus.Type)

	// Check cronjob workload status
	cronJobStatus, exists := statuses["test-echo-cronjob"]
	require.True(t, exists, "Should have cronjob workload")
	require.Equal(t, common.WorkloadTypeCronJob, cronJobStatus.Type)

	// Check deployment workload status
	deploymentStatus, exists := statuses["test-echo-deployment"]
	require.True(t, exists, "Should have deployment workload")
	require.Equal(t, common.WorkloadTypeService, deploymentStatus.Type)

	// Stop the controller
	t.Log("Stopping controller...")
	stopCtx, stopCancel := context.WithTimeout(common.StoreStartTime(context.Background()), 35*time.Second)
	defer stopCancel()
	err = controller.Stop(stopCtx)
	require.NoError(t, err)

	// Wait for workloads to stop
	t.Log("Waiting for workloads to stop...")

	// Helper function to wait for resource to be deleted
	waitForResourceDeletion := func(resourceType, name string, checkFn func() (bool, error), timeout time.Duration) bool {
		return waitForCondition(timeout, func() bool {
			isDeleted, err := checkFn()
			if err != nil {
				t.Logf("Error checking if %s %s is deleted: %v", resourceType, name, err)
				return false
			}
			t.Logf("%s %s deleted: %v", resourceType, name, isDeleted)
			return isDeleted
		})
	}

	// Wait for process pod to be deleted
	processPodDeleted := waitForResourceDeletion("Pod", "test-echo-process-pod", func() (bool, error) {
		_, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "test-echo-process-pod", metav1.GetOptions{})
		return errors.IsNotFound(err), nil
	}, 19*time.Second)
	require.True(t, processPodDeleted, "Process pod should be deleted")

	// Wait for deployment to be scaled to 0
	deploymentScaledDown := env.waitForDeployment(t, "test-echo-deployment", 0, 19*time.Second)
	require.True(t, deploymentScaledDown, "Deployment should be scaled to 0")

	// Check if cronjob is suspended
	cronJob, err := env.GetClient().BatchV1().CronJobs(env.namespace).Get(ctx, "test-echo-cronjob", metav1.GetOptions{})
	if err == nil && cronJob.Spec.Suspend != nil {
		require.True(t, *cronJob.Spec.Suspend, "CronJob should be suspended")
	} else if !errors.IsNotFound(err) {
		t.Logf("Error getting cronjob: %v", err)
	}
}

func TestProcessConfigYAMLParsing(t *testing.T) {
	// Sample YAML that matches your test case
	yamlStr := `
- name: echo-process-0
  image: busybox:latest
  terminationGracePeriod: 5s
  script:
    commands:
      - "echo 'Starting echo process 0'"
      - "echo 'Process ID: $$'"
      - "sleep 5"
    shell: "/bin/sh"
  env:
    TEST_ENV: "value1"
  resources:
    cpuRequest: "250m"
    memoryRequest: "512Mi"
    cpuLimit: "500m"
    memoryLimit: "1Gi"
  restartPolicy: "Never"
  sidecars:
    - name: sidecar
      image: busybox:latest
      command: ["/bin/sh", "-c"]
      args: ["echo 'Starting sidecar process 0'; echo 'Sidecar Process ID: $$'; sleep 300"]
      env:
        TEST_ENV: "sidecar"
      resources:
        cpuRequest: "250m"
        memoryRequest: "512Mi"
        cpuLimit: "500m"
        memoryLimit: "1Gi"
`
	// Parse the YAML into ProcessConfig objects
	var configs []common.ProcessConfig
	err := yaml.Unmarshal([]byte(yamlStr), &configs)
	require.NoError(t, err, "YAML parsing should succeed")

	// Verify we got the expected number of configs
	require.Len(t, configs, 1, "Should parse 1 process config")

	// Verify the main process config
	config := configs[0]
	require.Equal(t, "echo-process-0", config.Name, "Process name should match")
	require.Equal(t, "busybox:latest", config.Image, "Image should match")

	// Verify resource requirements
	require.Equal(t, "250m", config.Resources.CPURequest, "CPU request should match")
	require.Equal(t, "512Mi", config.Resources.MemoryRequest, "Memory request should match")
	require.Equal(t, "500m", config.Resources.CPULimit, "CPU limit should match")
	require.Equal(t, "1Gi", config.Resources.MemoryLimit, "Memory limit should match")

	// Verify sidecars
	require.Len(t, config.Sidecars, 1, "Should have 1 sidecar")
	sidecar := config.Sidecars[0]
	require.Equal(t, "sidecar", sidecar.Name, "Sidecar name should match")
	require.Equal(t, "busybox:latest", sidecar.Image, "Sidecar image should match")

	// Verify sidecar resources
	require.Equal(t, "250m", sidecar.Resources.CPURequest, "Sidecar CPU request should match")
	require.Equal(t, "512Mi", sidecar.Resources.MemoryRequest, "Sidecar memory request should match")
	require.Equal(t, "500m", sidecar.Resources.CPULimit, "Sidecar CPU limit should match")
	require.Equal(t, "1Gi", sidecar.Resources.MemoryLimit, "Sidecar memory limit should match")

	// Test building resource requirements
	k8sResources := config.Resources.BuildResourceRequirements()
	require.NotNil(t, k8sResources.Requests, "Resource requests should not be nil")
	require.NotNil(t, k8sResources.Limits, "Resource limits should not be nil")

	// Verify CPU request
	cpuRequest, exists := k8sResources.Requests["cpu"]
	require.True(t, exists, "CPU request should exist")
	require.Equal(t, "250m", cpuRequest.String(), "CPU request value should match")

	// Verify memory request
	memRequest, exists := k8sResources.Requests["memory"]
	require.True(t, exists, "Memory request should exist")
	require.Equal(t, "512Mi", memRequest.String(), "Memory request value should match")

	// Verify CPU limit
	cpuLimit, exists := k8sResources.Limits["cpu"]
	require.True(t, exists, "CPU limit should exist")
	require.Equal(t, "500m", cpuLimit.String(), "CPU limit value should match")

	// Verify memory limit
	memLimit, exists := k8sResources.Limits["memory"]
	require.True(t, exists, "Memory limit should exist")
	require.Equal(t, "1Gi", memLimit.String(), "Memory limit value should match")

	// Test building containers
	labels, containers, err := config.BuildContainers(common.LocalLeaderInfo{IsLeader: true})
	require.NoError(t, err)
	require.NotEmpty(t, labels, "Labels should not be empty")
	require.Len(t, containers, 2, "Should have 2 containers (main + sidecar)")

	// Verify main container resources
	mainContainer := containers[0]
	require.Equal(t, "main", mainContainer.Name, "Main container name should be 'main'")
	require.NotNil(t, mainContainer.Resources.Requests, "Main container resource requests should not be nil")
	require.NotNil(t, mainContainer.Resources.Limits, "Main container resource limits should not be nil")

	// Verify sidecar container resources
	sidecarContainer := containers[1]
	require.Equal(t, "sidecar", sidecarContainer.Name, "Sidecar container name should match")
	require.NotNil(t, sidecarContainer.Resources.Requests, "Sidecar container resource requests should not be nil")
	require.NotNil(t, sidecarContainer.Resources.Limits, "Sidecar container resource limits should not be nil")
}

// TestProcessManagerWithMultipleProcesses tests the ProcessManager with multiple processes
func TestProcessManagerWithMultipleProcesses(t *testing.T) {
	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 100*time.Second)
	defer cancel()

	// Create a temporary directory for process configs
	tempDir, err := os.MkdirTemp("", "process-configs")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a process config file
	processConfigYAML := `
- name: echo-process-0
  image: busybox:latest
  terminationGracePeriod: 5s
  script:
    commands:
      - "echo 'Starting echo process 0'"
      - "echo 'Process ID: $$'"
      - "sleep 5"
  shell: "/bin/sh"
  env:
    TEST_ENV: "value1"
  resources:
    cpuRequest: "250m"
    memoryRequest: "32Mi"
    cpuLimit: "500m"
    memoryLimit: ""
  restartPolicy: "Never"
  sidecars:
    - name: sidecar
      image: busybox:latest
      command: ["/bin/sh", "-c"]
      args: ["echo 'Starting sidecar process 0'; echo 'Sidecar Process ID: $$'; sleep 300"]
      env:
        TEST_ENV: "sidecar"
      resources:
        cpuRequest: "250m"
        memoryRequest: "32Mi"
        cpuLimit: "500m"
        memoryLimit: "1Gi"
- name: echo-process-1
  image: busybox:latest
  terminationGracePeriod: 5s
  script:
    commands:
      - "echo 'Starting echo process 1'"
      - "echo 'Process ID: $$'"
      - "sleep 10"
  shell: "/bin/sh"
  env:
    TEST_ENV: "value1"
  sidecars:
    - name: sidecar
      image: busybox:latest
      command: ["/bin/sh", "-c"]
      args: ["echo 'Starting sidecar process 1'; echo 'Sidecar Process ID: $$'; sleep 300"]
      env:
        TEST_ENV: "sidecar"
      resources:
        cpuRequest: "250m"
        memoryRequest: "32Mi"
        cpuLimit: "500m"
        memoryLimit: "1Gi"
  resources:
    cpuRequest: "250m"
    memoryRequest: "32Mi"
    cpuLimit: "500m"
    memoryLimit: "1Gi"
  restartPolicy: "Never"
- name: echo-process-2
  image: busybox:latest
  terminationGracePeriod: 5s
  script:
    commands:
      - "echo 'Starting echo process 2'"
      - "echo 'Process ID: $$'"
      - "sleep 30"
  shell: "/bin/sh"
  env:
    TEST_ENV: "value2"
  sidecars:
    - name: sidecar
      image: busybox:latest
      command: ["/bin/sh", "-c"]
      args: ["echo 'Starting sidecar process 2'; echo 'Sidecar Process ID: $$'; sleep 300"]
      env:
        TEST_ENV: "sidecar"
      resources:
        cpuRequest: "250m"
        memoryRequest: "32Mi"
        cpuLimit: "500m"
        memoryLimit: "1Gi"
  resources:
    cpuRequest: "250m"
    memoryRequest: "32Mi"
    cpuLimit: "500m"
    memoryLimit: "1Gi"
  restartPolicy: "Never"`
	configFile := filepath.Join(tempDir, "process-echo.yaml")
	err = os.WriteFile(configFile, []byte(processConfigYAML), 0644)
	require.NoError(t, err)

	// Create a process manager
	processManager := process.NewProcessManager(env.namespace, tempDir, env.metrics, env.monitorServer)
	t.Logf("Created process manager with config dir: %s", tempDir)
	t.Logf("Config file path: %s", configFile)

	// Create a workload manager and add the process manager
	workloadMgr := env.NewManager()
	err = workloadMgr.AddWorkload("processes", processManager)
	require.NoError(t, err)

	// Create a controller with our workload manager
	controller := NewLeaderController(
		env.leaderStorage,
		"controller-with-process-manager",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"tenant",
	)

	// After starting the controller
	t.Logf("Controller started, waiting to become leader")

	// Start the controller
	err = controller.Start(ctx)

	// Skip in mock mode -- we just need to test until start
	if os.Getenv("HIGHLANDER_TEST_MODE") != "real" {
		t.Skip("Skipping integration test in mock mode")
	}

	require.NoError(t, err)

	// Wait for the controller to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// After becoming leader
	t.Logf("Controller is now leader, waiting for processes to start")

	// Wait for processes to start
	time.Sleep(5 * time.Second)

	// Try to list all pods in the namespace
	pods, err := env.GetClient().CoreV1().Pods(env.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Logf("Error listing pods: %v", err)
	} else {
		t.Logf("Found %d pods in namespace", len(pods.Items))
		for _, pod := range pods.Items {
			t.Logf("Pod: %s, Status: %s", pod.Name, pod.Status.Phase)
		}
	}

	// Check process manager status
	processStatuses := processManager.GetStatus()
	t.Logf("Process manager status: %+v", processStatuses)

	// Verify processes were started
	//pod1, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "echo-process-1-pod", metav1.GetOptions{})
	//pod2, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "echo-process-2-pod", metav1.GetOptions{})

	success1 := env.waitForPodState(t, "echo-process-1-pod", true, 21*time.Second)
	require.True(t, success1, "Process 1 pod should be running")

	success2 := env.waitForPodState(t, "echo-process-2-pod", true, 21*time.Second)
	require.True(t, success2, "Process 2 pod should be running")

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)

	// Verify processes were stopped
	success = env.waitForPodState(t, "echo-process-1-pod", false, 18*time.Second)
	require.True(t, success, "Process 1 pod should be deleted")
	success = env.waitForPodState(t, "echo-process-2-pod", false, 18*time.Second)
	require.True(t, success, "Process 2 pod should be deleted")
}

// TestLeaderFailoverWithWorkloads tests leader failover with workloads
func TestLeaderFailoverWithRealWorkloads(t *testing.T) {
	// Skip in mock mode
	if os.Getenv("HIGHLANDER_TEST_MODE") != "real" {
		t.Skip("Skipping integration test in mock mode")
	}

	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 35*time.Second)
	defer cancel()

	// Create workload managers for each controller
	workloadMgr1 := env.NewManager()
	workloadMgr2 := env.NewManager()

	// Add a simple process workload to both managers
	processConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "failover-test-process",
			Namespace:              env.namespace,
			Image:                  "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"echo 'Starting failover test process'",
					"echo 'Process ID: $$'",
					"sleep 600", // Sleep for 10 minutes
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"TEST_ENV": "failover-test",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "100m",
				MemoryRequest: "64Mi",
				CPULimit:      "100m",
				MemoryLimit:   "64Mi",
			},
			RestartPolicy: "Never",
		},
	}

	processWorkload1, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer)
	require.NoError(t, err)
	err = workloadMgr1.AddWorkload(processWorkload1.GetName(), processWorkload1)
	require.NoError(t, err)

	processWorkload2, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer)
	require.NoError(t, err)
	err = workloadMgr2.AddWorkload(processWorkload2.GetName(), processWorkload2)
	require.NoError(t, err)

	// Create controllers
	controller1 := NewLeaderController(
		env.leaderStorage,
		"failover-controller-1",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr1,
		"failover-test",
	)

	controller2 := NewLeaderController(
		env.leaderStorage,
		"failover-controller-2",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr2,
		"failover-test",
	)

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

	// Wait for the process to start
	time.Sleep(5 * time.Second)

	// Verify process was started
	pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-test-process-pod", metav1.GetOptions{})
	require.NoError(t, err, "Process pod should exist")
	require.Equal(t, "Running", string(pod.Status.Phase), "Process pod should be running")

	// Stop controller 1 to simulate failure
	t.Log("Stopping controller 1 to simulate failure")
	err = controller1.Stop(ctx)
	require.NoError(t, err)

	// Wait for controller 2 to become leader
	success = waitForCondition(16*time.Second, func() bool {
		return controller2.IsLeader()
	})
	require.True(t, success, "Controller 2 should become leader after controller 1 failure")

	// Wait for the process to start on controller 2
	time.Sleep(5 * time.Second)

	// Verify process was stopped and restarted
	// First, check if the old pod is gone
	_, err = env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-test-process-pod", metav1.GetOptions{})
	if err != nil {
		// If there's an error, it should be because the pod is not found
		require.True(t, errors.IsNotFound(err), "Process pod should be deleted from cluster: %s", err)
	} else {
		// If the pod still exists, it should be a new pod (check creation timestamp)
		newPod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "failover-test-process-pod", metav1.GetOptions{})
		require.NoError(t, err, "Process pod should exist")
		require.Equal(t, "Running", string(newPod.Status.Phase), "Process pod should be running")

		// The pod should have been recreated, so it should have a recent creation timestamp
		require.True(t, newPod.CreationTimestamp.Time.After(pod.CreationTimestamp.Time),
			"New pod should have been created after the old one")
	}

	// Stop controller 2
	err = controller2.Stop(ctx)
	require.NoError(t, err)

	success = env.waitForPodState(t, "failover-test-process-pod", false, 18*time.Second)
	require.True(t, success, "Process pod should be deleted")
}

// TestRealWorldScenario tests a real-world scenario with multiple workload types
func TestRealWorldScenario(t *testing.T) {
	// Skip in mock mode
	if os.Getenv("HIGHLANDER_TEST_MODE") != "real" {
		t.Skip("Skipping integration test in mock mode")
	}

	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 35*time.Second)
	defer cancel()

	// Create a temporary directory for configs
	tempDir, err := os.MkdirTemp("", "workload-configs")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create process config file
	processConfigYAML := `
- name: abc-processor
  terminationGracePeriod: 5s
  image: busybox:latest
  script:
    commands:
      - "echo 'Starting ABC processor'"
      - "while true; do echo 'Processing ABC events at '$(date); sleep 10; done"
  shell: "/bin/sh"
  env:
    ABC_SOURCE: "postgres"
    ABC_TARGET: "kafka"
  resources:
    cpuRequest: "100m"
    memoryRequest: "64Mi"
  restartPolicy: "Always"`
	processConfigFile := filepath.Join(tempDir, "process-abc.yaml")
	err = os.WriteFile(processConfigFile, []byte(processConfigYAML), 0644)
	require.NoError(t, err)

	// Create cronjob config file
	cronJobConfigYAML := `
- name: data-processor
  schedule: "*/5 * * * *"
  image: busybox:latest
  script:
    commands:
      - "echo 'Processing data at '$(date)"
      - "sleep 5"
  shell: "/bin/sh"
  env:
    ENVIRONMENT: "production"
  restartPolicy: "OnFailure"
  resources:
    cpuRequest: "50m"
    memoryRequest: "32Mi"`
	cronJobConfigFile := filepath.Join(tempDir, "cronjob-data.yaml")
	err = os.WriteFile(cronJobConfigFile, []byte(cronJobConfigYAML), 0644)
	require.NoError(t, err)

	// Create a workload manager
	workloadMgr := env.NewManager()

	// Add a process manager
	processManager := process.NewProcessManager(env.namespace, tempDir, env.metrics, env.monitorServer)
	err = workloadMgr.AddWorkload("processes", processManager)
	require.NoError(t, err)

	// Add a cronjob manager
	cronJobManager := cronjob.NewCronJobManager(env.namespace, tempDir, env.metrics, env.monitorServer)
	err = workloadMgr.AddWorkload("cronjobs", cronJobManager)
	require.NoError(t, err)

	// Add a deployment workload (like Jobs Manager)
	deploymentConfig := common.ServiceConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "jobs-scheduler",
			Namespace:              env.namespace,
			Replicas:               1,
			Image:                  "busybox:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"while true; do echo 'Scheduler running at: '$(date); sleep 60; done",
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"JOBS_HOME": "/opt/jobs",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "200m",
				MemoryRequest: "256Mi",
				CPULimit:      "200m",
				MemoryLimit:   "256Mi",
			},
		},
	}
	deploymentWorkload, err := service.NewServiceWorkload(deploymentConfig, env.metrics, env.monitorServer)
	require.NoError(t, err)
	err = workloadMgr.AddWorkload(deploymentWorkload.GetName(), deploymentWorkload)
	require.NoError(t, err)

	// Create a controller with our workload manager
	controller := NewLeaderController(
		env.leaderStorage,
		"real-world-controller",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"production",
	)

	// Start the controller
	err = controller.Start(ctx)
	require.NoError(t, err)

	// Wait for the controller to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// Verify ABC process was started
	//abcPod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "abc-processor-pod", metav1.GetOptions{})
	success = env.waitForPodState(t, "abc-processor-pod", true, 15*time.Second)
	require.True(t, success, "ABC process pod should be running")

	// Verify cronjob was created
	//cronJob, err := env.GetClient().BatchV1().CronJobs(env.namespace).Get(ctx, "data-processor", metav1.GetOptions{})
	success = env.waitForCronJob(t, "data-processor", false, 15*time.Second)
	require.True(t, success, "CronJob should exist and not be suspended")

	// Verify deployment was created
	//deployment, err := env.GetClient().AppsV1().Deployments(env.namespace).Get(ctx, "jobs-scheduler", metav1.GetOptions{})
	success = env.waitForDeployment(t, "jobs-scheduler", 1, 15*time.Second)
	require.True(t, success, "Deployment should exist with 1 replica")

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)

	success = env.waitForPodState(t, "abc-processor-pod", false, 18*time.Second)
	require.True(t, success, "Process pod should be deleted")

	// Verify cronjob was suspended
	cronJob, err := env.GetClient().BatchV1().CronJobs(env.namespace).Get(context.Background(), "data-processor", metav1.GetOptions{})
	if err == nil {
		require.True(t, *cronJob.Spec.Suspend, "CronJob should be suspended")
	}

	// Verify deployment was scaled to 0
	deployment, err := env.GetClient().AppsV1().Deployments(env.namespace).Get(context.Background(), "jobs-scheduler", metav1.GetOptions{})
	if err == nil {
		require.Equal(t, int32(0), *deployment.Spec.Replicas, "Deployment should be scaled to 0")
	}
}

// TestDockerCommandExecution tests executing commands in Docker containers
func TestDockerCommandExecution(t *testing.T) {
	// Skip in mock mode
	if os.Getenv("HIGHLANDER_TEST_MODE") != "real" {
		t.Skip("Skipping integration test in mock mode")
	}

	// Check if Docker is available
	_, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker not available, skipping test")
	}

	env := NewTestEnv(t, "")
	defer env.Cleanup()

	ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 35*time.Second)
	defer cancel()

	// Create a workload manager
	workloadMgr := env.NewManager()

	// Add a process workload that runs a Docker container
	processConfig := common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			TerminationGracePeriod: time.Second * 5,
			Name:                   "docker-test",
			Namespace:              env.namespace,
			Image:                  "alpine:latest",
			Script: &common.ScriptConfig{
				Commands: []string{
					"echo 'Starting Docker test'",
					"apk add --no-cache curl",
					"curl -s https://httpbin.org/get | grep origin",
					"echo 'Test completed successfully'",
					"sleep 60", // Keep container running for a while
				},
				Shell: "/bin/sh",
			},
			Env: map[string]string{
				"TEST_ENV": "docker-test",
			},
			Resources: common.ResourceConfig{
				CPURequest:    "100m",
				MemoryRequest: "64Mi",
				CPULimit:      "200m",
				MemoryLimit:   "256Mi",
			},
			RestartPolicy: "Never",
		},
	}

	processWorkload, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer)
	require.NoError(t, err)
	err = workloadMgr.AddWorkload(processWorkload.GetName(), processWorkload)
	require.NoError(t, err)

	// Create a controller with our workload manager
	controller := NewLeaderController(
		env.leaderStorage,
		"docker-test-controller",
		&env.cluster,
		env.metrics,
		env.monitorServer,
		workloadMgr,
		"docker-test",
	)

	// Start the controller
	err = controller.Start(ctx)
	require.NoError(t, err)

	// Wait for the controller to become leader
	success := waitForCondition(5*time.Second, func() bool {
		return controller.IsLeader()
	})
	require.True(t, success, "Controller should become leader")

	// Wait for the container to start and execute commands
	time.Sleep(15 * time.Second)

	// Verify the pod is running
	pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "docker-test-pod", metav1.GetOptions{})
	require.NoError(t, err, "Docker test pod should exist")
	require.Equal(t, "Running", string(pod.Status.Phase), "Docker test pod should be running")

	// Get logs from the pod to verify command execution
	logs, err := env.GetClient().CoreV1().Pods(env.namespace).GetLogs("docker-test-pod", &corev1.PodLogOptions{}).Do(ctx).Raw()
	require.NoError(t, err, "Should be able to get pod logs")

	// Check if the logs contain expected output
	logsStr := string(logs)
	require.Contains(t, logsStr, "Starting Docker test", "Logs should contain start message")
	require.Contains(t, logsStr, "origin", "Logs should contain curl output")

	// Stop the controller
	err = controller.Stop(ctx)
	require.NoError(t, err)

	success = env.waitForPodState(t, "docker-test-pod", false, 18*time.Second)
	require.True(t, success, "Docker test pod should be deleted")
}

// TestStorageImplementationComparison tests both Redis and DB storage implementations
func TestStorageImplementationComparison(t *testing.T) {
	// Skip in mock mode
	if os.Getenv("HIGHLANDER_TEST_MODE") != "real" {
		t.Skip("Skipping integration test in mock mode")
	}

	// Test with Redis storage
	t.Run("Redis", func(t *testing.T) {
		_ = os.Setenv("HIGHLANDER_STORAGE_TYPE", "redis")
		defer os.Unsetenv("HIGHLANDER_STORAGE_TYPE")

		env := NewTestEnv(t, "")
		defer env.Cleanup()

		ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 10*time.Second)
		defer cancel()

		// Create a simple process workload
		workloadMgr := env.NewManager()
		processConfig := common.ProcessConfig{
			BaseWorkloadConfig: common.BaseWorkloadConfig{
				TerminationGracePeriod: time.Second * 5,
				Name:                   "redis-test-process",
				Namespace:              env.namespace,
				Image:                  "busybox:latest",
				Script: &common.ScriptConfig{
					Commands: []string{
						"echo 'Redis storage test'",
						"sleep 300",
					},
					Shell: "/bin/sh",
				},
				RestartPolicy: "Never",
			},
		}

		processWorkload, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer)
		require.NoError(t, err)
		err = workloadMgr.AddWorkload(processWorkload.GetName(), processWorkload)
		require.NoError(t, err)

		// Create a controller with Redis storage
		controller := NewLeaderController(
			env.leaderStorage,
			"redis-storage-controller",
			&env.cluster,
			env.metrics,
			env.monitorServer,
			workloadMgr,
			"storage-test",
		)

		// Start the controller
		err = controller.Start(ctx)
		require.NoError(t, err)

		// Wait for the controller to become leader
		success := waitForCondition(5*time.Second, func() bool {
			return controller.IsLeader()
		})
		require.True(t, success, "Controller should become leader with Redis storage")

		// Wait for the process to start
		time.Sleep(5 * time.Second)

		// Verify the pod is running
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "redis-test-process-pod", metav1.GetOptions{})
		require.NoError(t, err, "Process pod should exist with Redis storage")
		require.Equal(t, "Running", string(pod.Status.Phase), "Process pod should be running with Redis storage")

		// Stop the controller
		err = controller.Stop(ctx)
		require.NoError(t, err)

		// Verify the pod is deleted
		success = env.waitForPodState(t, "redis-test-process-pod", false, 18*time.Second)
		require.True(t, success, "Process pod should be deleted with Redis storage")
	})

	// Test with DB storage
	t.Run("DB", func(t *testing.T) {
		_ = os.Setenv("HIGHLANDER_STORAGE_TYPE", "db")
		defer os.Unsetenv("HIGHLANDER_STORAGE_TYPE")

		env := NewTestEnv(t, "")
		defer env.Cleanup()

		ctx, cancel := context.WithTimeout(common.StoreStartTime(context.Background()), 10*time.Second)
		defer cancel()

		// Create a simple process workload
		workloadMgr := env.NewManager()
		processConfig := common.ProcessConfig{
			BaseWorkloadConfig: common.BaseWorkloadConfig{
				TerminationGracePeriod: time.Second * 5,
				Name:                   "db-test-process",
				Namespace:              env.namespace,
				Image:                  "busybox:latest",
				Script: &common.ScriptConfig{
					Commands: []string{
						"echo 'DB storage test'",
						"sleep 300",
					},
					Shell: "/bin/sh",
				},
				RestartPolicy: "Never",
			},
		}

		processWorkload, err := process.NewProcessWorkload(processConfig, env.metrics, env.monitorServer)
		require.NoError(t, err)
		err = workloadMgr.AddWorkload(processWorkload.GetName(), processWorkload)
		require.NoError(t, err)

		// Create a controller with DB storage
		controller := NewLeaderController(
			env.leaderStorage,
			"db-storage-controller",
			&env.cluster,
			env.metrics,
			env.monitorServer,
			workloadMgr,
			"storage-test",
		)

		// Start the controller
		err = controller.Start(ctx)
		require.NoError(t, err)

		// Wait for the controller to become leader
		success := waitForCondition(5*time.Second, func() bool {
			return controller.IsLeader()
		})
		require.True(t, success, "Controller should become leader with DB storage")

		// Wait for the process to start
		time.Sleep(5 * time.Second)

		// Verify the pod is running
		pod, err := env.GetClient().CoreV1().Pods(env.namespace).Get(ctx, "db-test-process-pod", metav1.GetOptions{})
		require.NoError(t, err, "Process pod should exist with DB storage")
		require.Equal(t, "Running", string(pod.Status.Phase), "Process pod should be running with DB storage")

		// Stop the controller
		err = controller.Stop(ctx)
		require.NoError(t, err)

		// Verify the pod is deleted
		success = env.waitForPodState(t, "db-test-process-pod", false, 18*time.Second)
		require.True(t, success, "Process pod should be deleted with DB storage")
	})
}

//func TestMain(m *testing.M) {
//	// Disable parallel test execution for integration tests
//	flag.Set("test.parallel", "1")
//
//	// Run tests
//	os.Exit(m.Run())
//}
