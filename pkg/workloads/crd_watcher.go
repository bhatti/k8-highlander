package workloads

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	"strings"
	"sync"
	"time"

	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// CRDWatcher watches for workload CRD changes
type CRDWatcher struct {
	dynamicClient    dynamic.Interface
	k8sClient        kubernetes.Interface
	stopCh           chan struct{}
	resyncPeriod     time.Duration
	manager          Manager
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer

	// Track which informers are running
	informers      []cache.SharedIndexInformer
	informersMutex sync.Mutex

	// Track currently managed CRDs to avoid duplicates
	managedCRDs map[string]bool
	crdsMutex   sync.RWMutex
}

// NewCRDWatcher creates a new CRD watcher
func NewCRDWatcher(
	dynamicClient dynamic.Interface,
	k8sClient kubernetes.Interface,
	manager Manager,
	metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer,
) *CRDWatcher {
	return &CRDWatcher{
		dynamicClient:    dynamicClient,
		k8sClient:        k8sClient,
		stopCh:           make(chan struct{}),
		resyncPeriod:     time.Minute * 5,
		manager:          manager,
		metrics:          metrics,
		monitoringServer: monitoringServer,
		informers:        make([]cache.SharedIndexInformer, 0),
		managedCRDs:      make(map[string]bool),
	}
}

// Start starts watching for CRD changes
func (w *CRDWatcher) Start(ctx context.Context) error {
	klog.Infof("Starting CRD watcher (leader: %v)", w.monitoringServer.GetLeaderInfo())

	// Define the group-version-resources to watch
	resources := []schema.GroupVersionResource{
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ProcessesResource},
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ServicesResource},
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.CronjobsResource},
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.PersistentsResource},
	}

	// Reset stop channel if already closed
	select {
	case <-w.stopCh:
		w.stopCh = make(chan struct{})
	default:
		// Channel is already open
	}

	// Setup factory and informers for each resource type
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		w.dynamicClient, w.resyncPeriod, metav1.NamespaceAll, nil)

	w.informersMutex.Lock()
	defer w.informersMutex.Unlock()

	// Clear existing informers
	w.informers = make([]cache.SharedIndexInformer, 0, len(resources))

	for _, gvr := range resources {
		informer := factory.ForResource(gvr).Informer()

		_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    w.OnCRDAdd,
			UpdateFunc: w.OnCRDUpdate,
			DeleteFunc: w.OnCRDDelete,
		})

		w.informers = append(w.informers, informer)

		go informer.Run(w.stopCh)
	}

	// Wait for all informer caches to sync
	for i, informer := range w.informers {
		if !cache.WaitForCacheSync(w.stopCh, informer.HasSynced) {
			klog.Errorf("Failed to sync informer cache for %s", resources[i].String())
			return fmt.Errorf("failed to sync informer cache for %s", resources[i].String())
		}
	}

	// Perform initial sync of existing CRDs
	klog.V(4).Infof("CRD watcher started, performing initial sync (leader: %v)", w.monitoringServer.GetLeaderInfo())
	if err := w.performInitialSync(ctx); err != nil {
		klog.Warningf("Initial CRD sync encountered errors: %v", err)
	}

	return nil
}

// Stop stops the CRD watcher
func (w *CRDWatcher) Stop(context.Context) error {
	klog.Infof("Stopping CRD watcher (leader: %v)", w.monitoringServer.GetLeaderInfo())

	select {
	case <-w.stopCh:
		// Already stopped
		klog.Infof("CRD watcher was already stopped (leader: %v)", w.monitoringServer.GetLeaderInfo())
	default:
		// Close stop channel to signal informers to stop
		close(w.stopCh)
		klog.Infof("CRD watcher stopped (leader: %v)", w.monitoringServer.GetLeaderInfo())
	}

	return nil
}

// OnCRDAdd handles CRD addition events
func (w *CRDWatcher) OnCRDAdd(obj interface{}) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		klog.Errorf("Expected *unstructured.Unstructured, got %T", obj)
		return
	}

	count := w.processCRDChange(context.Background(), unstructuredObj, "add")
	klog.Infof("OnCRDAdd added %d (leader: %v)", count, w.monitoringServer.GetLeaderInfo())
}

// OnCRDUpdate handles CRD update events
func (w *CRDWatcher) OnCRDUpdate(oldObj, newObj interface{}) {
	unstructuredNew, ok := newObj.(*unstructured.Unstructured)
	if !ok {
		klog.Errorf("Expected *unstructured.Unstructured, got %T", newObj)
		return
	}

	// Check if it's a significant update by comparing resource versions
	unstructuredOld, ok := oldObj.(*unstructured.Unstructured)
	if ok && unstructuredOld.GetResourceVersion() != "" && unstructuredNew.GetResourceVersion() != "" &&
		unstructuredOld.GetResourceVersion() == unstructuredNew.GetResourceVersion() {
		// No actual change, skip processing
		klog.Infof("OnCRDUpdate did not see version change %s == %s (leader: %v)",
			unstructuredOld.GetResourceVersion(), unstructuredNew.GetResourceVersion(),
			w.monitoringServer.GetLeaderInfo())
		return
	}

	count := w.processCRDChange(context.Background(), unstructuredNew, "update")
	klog.Infof("OnCRDUpdate updated %d version %s (leader: %v)", count, unstructuredNew.GetResourceVersion(), w.monitoringServer.GetLeaderInfo())
}

// OnCRDDelete handles CRD deletion events
func (w *CRDWatcher) OnCRDDelete(obj interface{}) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		// Handle delete events with tombstone
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			if unstructuredObj, ok = tombstone.Obj.(*unstructured.Unstructured); ok {
				// Process with the unstructured object from tombstone
			} else {
				klog.Errorf("Tombstone contained unexpected object: %#v", tombstone.Obj)
				return
			}
		} else {
			klog.Errorf("Expected *unstructured.Unstructured, got %T", obj)
			return
		}
	}

	count := w.processCRDChange(context.Background(), unstructuredObj, "delete")
	klog.Infof("OnCRDDelete deleted %d (leader: %v)", count, w.monitoringServer.GetLeaderInfo())
}

// processCRDChange handles all CRD changes
func (w *CRDWatcher) processCRDChange(ctx context.Context, obj *unstructured.Unstructured, changeType string) int {
	if !w.monitoringServer.IsLeaderAndNormal() {
		klog.V(4).Infof("Not the leader, skipping process of CRD %s/%s (leader %v)",
			obj.GetNamespace(), obj.GetName(), w.monitoringServer.GetLeaderInfo())
		return 0
	}
	crdRef := &common.WorkloadCRDReference{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
	}

	// Create a CRD key for tracking
	crdKey := fmt.Sprintf("%s/%s/%s/%s",
		crdRef.APIVersion, crdRef.Kind, crdRef.Namespace, crdRef.Name)

	switch changeType {
	case "add", "update":
		klog.Infof("Processing %s of CRD %s/%s of kind %s (leader: %v)",
			changeType, crdRef.Namespace, crdRef.Name, crdRef.Kind, w.monitoringServer.GetLeaderInfo())

		w.crdsMutex.Lock()
		w.managedCRDs[crdKey] = true
		w.crdsMutex.Unlock()

		if count, err := w.syncWorkloadFromCRD(ctx, crdRef); err != nil {
			klog.Errorf("Failed to sync workload from CRD %s: %v (leader: %v)",
				crdKey, err, w.monitoringServer.GetLeaderInfo())

			if errors.IsNotFound(err) || errors.IsAlreadyExists(err) {
				// These are expected in some cases
				w.metrics.RecordWorkloadOperation(
					fmt.Sprintf("crd-%s", crdRef.Kind),
					fmt.Sprintf("%s/%s", crdRef.Namespace, crdRef.Name),
					"sync-expected-error",
					0,
					err,
				)
			} else {
				w.metrics.RecordWorkloadOperation(
					fmt.Sprintf("crd-%s", crdRef.Kind),
					fmt.Sprintf("%s/%s", crdRef.Namespace, crdRef.Name),
					"sync-error",
					0,
					err,
				)
			}

			if w.metrics != nil {
				w.metrics.RecordWorkloadOperation(
					fmt.Sprintf("crd-%s", crdRef.Kind),
					fmt.Sprintf("%s/%s", crdRef.Namespace, crdRef.Name),
					"sync",
					0,
					err,
				)
			}
		} else {
			return count
		}

	case "delete":
		klog.Infof("Processing deletion of CRD %s/%s of kind %s (leader %v)",
			crdRef.Namespace, crdRef.Name, crdRef.Kind, w.monitoringServer.GetLeaderInfo())

		w.crdsMutex.Lock()
		delete(w.managedCRDs, crdKey)
		w.crdsMutex.Unlock()

		if err := w.removeWorkloadWithCRDRef(ctx, crdRef); err != nil {
			klog.Errorf("Failed to remove workload with CRD ref %s: %v (leader %v)",
				crdKey, err, w.monitoringServer.GetLeaderInfo())

			if w.metrics != nil {
				w.metrics.RecordWorkloadOperation(
					fmt.Sprintf("crd-%s", crdRef.Kind),
					fmt.Sprintf("%s/%s", crdRef.Namespace, crdRef.Name),
					"remove",
					0,
					err,
				)
			}
		}
		return -1
	}
	return 0
}

// syncWorkloadFromCRD syncs a workload from a CRD reference
func (w *CRDWatcher) syncWorkloadFromCRD(ctx context.Context, crdRef *common.WorkloadCRDReference) (count int, err error) {
	startTime := time.Now()

	// Check if we're the leader - if not, don't sync workloads
	if !w.isLeaderActive() {
		klog.V(4).Infof("Not the leader, skipping sync of CRD %s/%s", crdRef.Namespace, crdRef.Name)
		return 0, nil
	}

	// Load the workload configuration from the CRD
	workloadConfig, err := common.LoadWorkloadFromCRD(ctx, w.dynamicClient, crdRef)
	if err != nil {
		return 0, fmt.Errorf("failed to load workload from CRD: %w", err)
	}

	// Create a workload name based on the configuration
	var workloadName string
	var workloadType common.WorkloadType

	switch config := workloadConfig.(type) {
	case *common.ProcessConfig:
		workloadName = config.Name
		workloadType = common.WorkloadTypeProcess
		// Find the process cronjobsManager
		processManager := w.manager.GetWorkloadManager(common.WorkloadTypeProcessManager)
		// Check if we already have this process
		existingProcess, exists := processManager.GetWorkload(workloadName)
		klog.V(4).Infof("checking existing load for %s=%s", workloadName, existingProcess)

		if exists {
			// Stop the existing process
			if err = existingProcess.Stop(ctx); err != nil {
				klog.Warningf("Error stopping existing process %s: %v", workloadName, err)
			}

			// Remove from cronjobsManager
			if err := processManager.RemoveWorkload(workloadName); err != nil {
				klog.Warningf("Error removing process %s: %v", workloadName, err)
			}
		}

		// Add the new process
		if err = processManager.AddWorkload(*config); err != nil {
			return 0, fmt.Errorf("failed to add process from CRD: %w", err)
		}
		count = 1

		klog.Infof("Successfully synced process %s from CRD (leader %v)", workloadName, w.monitoringServer.GetLeaderInfo())
		break

	case *common.ServiceConfig:
		workloadName = config.Name
		workloadType = common.WorkloadTypeService
		serviceManager := w.manager.GetWorkloadManager(common.WorkloadTypeServiceManager)

		// Check if we already have this deployment
		existingDeployment, exists := serviceManager.GetWorkload(workloadName)

		if exists {
			// Stop the existing deployment
			if err := existingDeployment.Stop(ctx); err != nil {
				klog.Warningf("Error stopping existing deployment %s: %v", workloadName, err)
			}

			// Remove from cronjobsManager
			if err := serviceManager.RemoveWorkload(workloadName); err != nil {
				klog.Warningf("Error removing deployment %s: %v", workloadName, err)
			}
		}

		// Add the new deployment
		if err := serviceManager.AddWorkload(*config); err != nil {
			return 0, fmt.Errorf("failed to add deployment from CRD: %w", err)
		}
		count = 1

		klog.Infof("Successfully synced deployment %s from CRD (leader %v)", workloadName, w.monitoringServer.GetLeaderInfo())
		break

	case *common.CronJobConfig:
		workloadName = config.Name
		workloadType = common.WorkloadTypeCronJob
		cronjobsManager := w.manager.GetWorkloadManager(common.WorkloadTypeCronJobManager)

		// Check if we already have this cron job
		existingCronJob, exists := cronjobsManager.GetWorkload(workloadName)

		if exists {
			// Stop the existing cron job
			if err := existingCronJob.Stop(ctx); err != nil {
				klog.Warningf("Error stopping existing cron job %s: %v", workloadName, err)
			}

			// Remove from cronjobsManager
			if err := cronjobsManager.RemoveWorkload(workloadName); err != nil {
				klog.Warningf("Error removing cron job %s: %v", workloadName, err)
			}
		}

		// Add the new cron job
		if err := cronjobsManager.AddWorkload(*config); err != nil {
			return 0, fmt.Errorf("failed to add cron job from CRD: %w", err)
		}
		count = 1

		klog.Infof("Successfully synced cron job %s from CRD (leader %v)", workloadName, w.monitoringServer.GetLeaderInfo())
		break

	case *common.PersistentConfig:
		workloadName = config.Name
		workloadType = common.WorkloadTypePersistent
		persistentManager := w.manager.GetWorkloadManager(common.WorkloadTypePersistentManager)

		// Check if we already have this stateful set
		existingStatefulSet, exists := persistentManager.GetWorkload(workloadName)

		if exists {
			// Stop the existing stateful set
			if err := existingStatefulSet.Stop(ctx); err != nil {
				klog.Warningf("Error stopping existing stateful set %s: %v", workloadName, err)
			}

			// Remove from cronjobsManager
			if err := persistentManager.RemoveWorkload(workloadName); err != nil {
				klog.Warningf("Error removing stateful set %s: %v", workloadName, err)
			}
		}

		// Add the new stateful set
		if err := persistentManager.AddWorkload(*config); err != nil {
			return 0, fmt.Errorf("failed to add stateful set from CRD: %w", err)
		}
		count = 1

		klog.Infof("Successfully synced stateful set %s from CRD (leader %v)", workloadName, w.monitoringServer.GetLeaderInfo())
		break
	}

	// Record metric for sync operation
	if w.metrics != nil {
		duration := time.Since(startTime)
		w.metrics.RecordWorkloadOperation(
			string(workloadType),
			workloadName,
			"sync-crd",
			duration,
			nil,
		)
	}

	// If the cronjobsManager is active (i.e., leader is active), start the workload
	if w.isLeaderActive() {
		return count, w.startWorkloadByTypeName(ctx, workloadType, workloadName)
	}

	return count, nil
}

// removeWorkloadWithCRDRef removes a workload that references a deleted CRD
func (w *CRDWatcher) removeWorkloadWithCRDRef(ctx context.Context, crdRef *common.WorkloadCRDReference) error {
	// Search for workloads with this CRD reference
	workloads := w.findWorkloadsByCRDRef(crdRef)

	if len(workloads) == 0 {
		klog.Infof("No workloads found for deleted CRD %s/%s of kind %s (leader %v)",
			crdRef.Namespace, crdRef.Name, crdRef.Kind, w.monitoringServer.GetLeaderInfo())
		return nil
	}

	for _, workload := range workloads {
		workloadName := workload.GetName()
		workloadType := workload.GetType()

		klog.Infof("Removing workload %s of type %s due to CRD deletion (leader %v)",
			workloadName, workloadType, w.monitoringServer.GetLeaderInfo())

		// Stop the workload if running
		if err := workload.Stop(ctx); err != nil {
			klog.Warningf("Error stopping workload %s: %v", workload.GetName(), err)
			// Continue with removal anyway
		}

		// Get the appropriate workload manager based on type
		var manager WorkloadManager
		switch workloadType {
		case common.WorkloadTypeProcess:
			manager = w.manager.GetWorkloadManager(common.WorkloadTypeProcessManager)
		case common.WorkloadTypeService:
			manager = w.manager.GetWorkloadManager(common.WorkloadTypeServiceManager)
		case common.WorkloadTypeCronJob:
			manager = w.manager.GetWorkloadManager(common.WorkloadTypeCronJobManager)
		case common.WorkloadTypePersistent:
			manager = w.manager.GetWorkloadManager(common.WorkloadTypePersistentManager)
		default:
			klog.Warningf("Unknown workload type %s for workload %s", workloadType, workloadName)
			continue
		}

		if manager == nil {
			klog.Warningf("No manager found for workload type %s", workloadType)
			continue
		}

		// Remove the workload from the manager
		if err := manager.RemoveWorkload(workloadName); err != nil {
			// If the workload doesn't exist, that's fine - it might have been removed during Stop
			if strings.Contains(err.Error(), "does not exist") {
				klog.Infof("Workload %s already removed during Stop", workloadName)
			} else {
				klog.Warningf("Error removing workload %s from manager: %v", workloadName, err)
				return err
			}
		}
	}

	return nil
}

// findWorkloadsByCRDRef finds workloads by their CRD reference
func (w *CRDWatcher) findWorkloadsByCRDRef(crdRef *common.WorkloadCRDReference) []common.Workload {
	result := make([]common.Workload, 0)

	managers := w.manager.GetWorkloadManagers()
	for _, manager := range managers {
		// Get all workloads from manager
		allWorkloads := manager.GetWorkloadsWithCRD()

		// Check each workload for matching CRD reference
		for _, workload := range allWorkloads {
			if w.workloadHasCRDRef(workload, crdRef) {
				result = append(result, workload)
			}
		}
	}

	return result
}

// workloadHasCRDRef checks if a workload has the given CRD reference
func (w *CRDWatcher) workloadHasCRDRef(workload common.Workload, crdRef *common.WorkloadCRDReference) bool {
	if workload == nil || crdRef == nil {
		return false
	}
	workloadConfig := workload.GetConfig()
	if workloadConfig.WorkloadCRDRef == nil {
		return false
	}

	// Compare CRD references
	return workloadConfig.WorkloadCRDRef.APIVersion == crdRef.APIVersion &&
		workloadConfig.WorkloadCRDRef.Kind == crdRef.Kind &&
		workloadConfig.WorkloadCRDRef.Name == crdRef.Name &&
		(workloadConfig.WorkloadCRDRef.Namespace == crdRef.Namespace ||
			(workloadConfig.WorkloadCRDRef.Namespace == "" && crdRef.Namespace == "default") ||
			(workloadConfig.WorkloadCRDRef.Namespace == "default" && crdRef.Namespace == ""))
}

// performInitialSync performs an initial sync of all existing CRDs
func (w *CRDWatcher) performInitialSync(ctx context.Context) error {
	klog.Infof("Performing initial sync of existing CRDs (leader %v)", w.monitoringServer.GetLeaderInfo())

	// Define the group-version-resources to sync
	resources := []schema.GroupVersionResource{
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ProcessesResource},
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.ServicesResource},
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.CronjobsResource},
		{Group: common.GROUP, Version: common.MajorVersion, Resource: common.PersistentsResource},
	}

	var syncErrors []error

	// Sync each resource type
	for _, gvr := range resources {
		resourceList, err := w.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			klog.Errorf("Error listing resources of type %s: %v", gvr.String(), err)
			syncErrors = append(syncErrors, fmt.Errorf("error listing %s: %w", gvr.String(), err))
			continue
		}

		for _, item := range resourceList.Items {
			klog.Infof("Found existing CRD %s/%s of kind %s", item.GetNamespace(), item.GetName(), item.GetKind())
			w.processCRDChange(ctx, &item, "add")
		}
	}

	if len(syncErrors) > 0 {
		return fmt.Errorf("initial sync encountered %d errors", len(syncErrors))
	}

	return nil
}

// isLeaderActive checks if the leader controller is active
func (w *CRDWatcher) isLeaderActive() bool {
	// Check if monitoring server is reporting that this instance is leader
	return w.monitoringServer != nil && w.monitoringServer.GetLeaderInfo().IsLeader
}

// startWorkloadByTypeName starts a workload by name and type
func (w *CRDWatcher) startWorkloadByTypeName(ctx context.Context, workloadType common.WorkloadType, workloadName string) error {
	var workload common.Workload
	var exists bool
	switch workloadType {
	case common.WorkloadTypeProcess:
		processManager := w.manager.GetWorkloadManager(common.WorkloadTypeProcessManager)
		workload, exists = processManager.GetWorkload(workloadName)
		break
	case common.WorkloadTypeService:
		serviceManager := w.manager.GetWorkloadManager(common.WorkloadTypeServiceManager)
		workload, exists = serviceManager.GetWorkload(workloadName)
		break
	case common.WorkloadTypeCronJob:
		cronjobsManager := w.manager.GetWorkloadManager(common.WorkloadTypeCronJobManager)
		workload, exists = cronjobsManager.GetWorkload(workloadName)
		break

	case common.WorkloadTypePersistent:
		persistentManager := w.manager.GetWorkloadManager(common.WorkloadTypePersistentManager)
		workload, exists = persistentManager.GetWorkload(workloadName)
		break
	}

	if !exists {
		return fmt.Errorf("workload %s/%s not found", workloadName, workloadType)
	}

	return workload.Start(ctx)
}
