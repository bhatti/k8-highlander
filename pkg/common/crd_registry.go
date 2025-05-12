package common

import (
	"context"
	"fmt"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

// CRDRegistry manages Custom Resource Definition registration and metadata.
type CRDRegistry struct {
	apiextensionsClient apiextensionsclientset.Interface
}

// NewCRDRegistry creates a new CRD registry.
func NewCRDRegistry(apiextensionsClient apiextensionsclientset.Interface) *CRDRegistry {
	return &CRDRegistry{
		apiextensionsClient: apiextensionsClient,
	}
}

// RegisterCRDs ensures all required CRDs are registered with the Kubernetes API server.
func (r *CRDRegistry) RegisterCRDs(ctx context.Context) error {
	// Define the list of CRDs we need to register
	crds := []*apiextensionsv1.CustomResourceDefinition{
		r.getWorkloadProcessCRD(),
		r.getWorkloadServiceCRD(),
		r.getWorkloadCronJobCRD(),
		r.getWorkloadPersistentCRD(),
	}

	// Register each CRD
	for _, crd := range crds {
		if err := r.ensureCRD(ctx, crd); err != nil {
			return err
		}
	}

	return nil
}

// ensureCRD creates or updates a CustomResourceDefinition.
func (r *CRDRegistry) ensureCRD(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition) error {
	existing, err := r.apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crd.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// CRD doesn't exist, create it
			klog.Infof("Creating CRD %s", crd.Name)
			_, err = r.apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create CRD %s: %w", crd.Name, err)
			}
			return nil
		}
		return fmt.Errorf("failed to check if CRD %s exists: %w", crd.Name, err)
	}

	// CRD exists, update it if needed
	// We only update the schema, preserving other fields to avoid conflicts
	existing.Spec.Versions = crd.Spec.Versions

	klog.Infof("Updating CRD %s", crd.Name)
	_, err = r.apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update CRD %s: %w", crd.Name, err)
	}

	return nil
}

// getWorkloadProcessCRD returns the WorkloadProcess CRD definition.
func (r *CRDRegistry) getWorkloadProcessCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workloadprocesses.highlander.plexobject.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: GROUP,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:     "workloadprocesses",
				Singular:   "workloadprocess",
				Kind:       "WorkloadProcess",
				ShortNames: []string{"wproc"},
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    MajorVersion,
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"image": {
											Type: "string",
										},
										"script": {
											Type: "object",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"commands": {
													Type: "array",
													Items: &apiextensionsv1.JSONSchemaPropsOrArray{
														Schema: &apiextensionsv1.JSONSchemaProps{
															Type: "string",
														},
													},
												},
												"shell": {
													Type: "string",
												},
											},
										},
										"env": {
											Type: "object",
											AdditionalProperties: &apiextensionsv1.JSONSchemaPropsOrBool{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "string",
												},
											},
										},
										"resources": {
											Type: "object",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"cpuRequest": {
													Type: "string",
												},
												"memoryRequest": {
													Type: "string",
												},
												"cpuLimit": {
													Type: "string",
												},
												"memoryLimit": {
													Type: "string",
												},
											},
										},
										"restartPolicy": {
											Type: "string",
										},
										"maxRestarts": {
											Type: "integer",
										},
										"gracePeriod": {
											Type: "string",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// getWorkloadServiceCRD returns the WorkloadService CRD definition.
func (r *CRDRegistry) getWorkloadServiceCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workloadservices.highlander.plexobject.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: GROUP,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:     "workloadservices",
				Singular:   "workloadservice",
				Kind:       "WorkloadService",
				ShortNames: []string{"wsvc"},
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    MajorVersion,
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"image": {
											Type: "string",
										},
										"script": {
											Type: "object",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"commands": {
													Type: "array",
													Items: &apiextensionsv1.JSONSchemaPropsOrArray{
														Schema: &apiextensionsv1.JSONSchemaProps{
															Type: "string",
														},
													},
												},
												"shell": {
													Type: "string",
												},
											},
										},
										"replicas": {
											Type: "integer",
										},
										"ports": {
											Type: "array",
											Items: &apiextensionsv1.JSONSchemaPropsOrArray{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "object",
													Properties: map[string]apiextensionsv1.JSONSchemaProps{
														"name": {
															Type: "string",
														},
														"containerPort": {
															Type: "integer",
														},
														"servicePort": {
															Type: "integer",
														},
													},
												},
											},
										},
										"env": {
											Type: "object",
											AdditionalProperties: &apiextensionsv1.JSONSchemaPropsOrBool{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "string",
												},
											},
										},
										"resources": {
											Type: "object",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"cpuRequest": {
													Type: "string",
												},
												"memoryRequest": {
													Type: "string",
												},
												"cpuLimit": {
													Type: "string",
												},
												"memoryLimit": {
													Type: "string",
												},
											},
										},
										"healthCheckPath": {
											Type: "string",
										},
										"healthCheckPort": {
											Type: "integer",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// getWorkloadCronJobCRD returns the WorkloadCronJob CRD definition.
func (r *CRDRegistry) getWorkloadCronJobCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workloadcronjobs.highlander.plexobject.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: GROUP,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:     "workloadcronjobs",
				Singular:   "workloadcronjob",
				Kind:       "WorkloadCronJob",
				ShortNames: []string{"wcron"},
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    MajorVersion,
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"image": {
											Type: "string",
										},
										"script": {
											Type: "object",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"commands": {
													Type: "array",
													Items: &apiextensionsv1.JSONSchemaPropsOrArray{
														Schema: &apiextensionsv1.JSONSchemaProps{
															Type: "string",
														},
													},
												},
												"shell": {
													Type: "string",
												},
											},
										},
										"schedule": {
											Type: "string",
										},
										"env": {
											Type: "object",
											AdditionalProperties: &apiextensionsv1.JSONSchemaPropsOrBool{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "string",
												},
											},
										},
										"resources": {
											Type: "object",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"cpuRequest": {
													Type: "string",
												},
												"memoryRequest": {
													Type: "string",
												},
												"cpuLimit": {
													Type: "string",
												},
												"memoryLimit": {
													Type: "string",
												},
											},
										},
										"restartPolicy": {
											Type: "string",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// getWorkloadPersistentCRD returns the WorkloadPersistent CRD definition.
func (r *CRDRegistry) getWorkloadPersistentCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workloadpersistents.highlander.plexobject.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: GROUP,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:     "workloadpersistents",
				Singular:   "workloadpersistent",
				Kind:       "WorkloadPersistent",
				ShortNames: []string{"wpers"},
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    MajorVersion,
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"image": {
											Type: "string",
										},
										"script": {
											Type: "object",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"commands": {
													Type: "array",
													Items: &apiextensionsv1.JSONSchemaPropsOrArray{
														Schema: &apiextensionsv1.JSONSchemaProps{
															Type: "string",
														},
													},
												},
												"shell": {
													Type: "string",
												},
											},
										},
										"replicas": {
											Type: "integer",
										},
										"ports": {
											Type: "array",
											Items: &apiextensionsv1.JSONSchemaPropsOrArray{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "object",
													Properties: map[string]apiextensionsv1.JSONSchemaProps{
														"name": {
															Type: "string",
														},
														"containerPort": {
															Type: "integer",
														},
														"servicePort": {
															Type: "integer",
														},
													},
												},
											},
										},
										"env": {
											Type: "object",
											AdditionalProperties: &apiextensionsv1.JSONSchemaPropsOrBool{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "string",
												},
											},
										},
										"resources": {
											Type: "object",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"cpuRequest": {
													Type: "string",
												},
												"memoryRequest": {
													Type: "string",
												},
												"cpuLimit": {
													Type: "string",
												},
												"memoryLimit": {
													Type: "string",
												},
											},
										},
										"serviceName": {
											Type: "string",
										},
										"healthCheckPath": {
											Type: "string",
										},
										"healthCheckPort": {
											Type: "integer",
										},
										"persistentVolumes": {
											Type: "array",
											Items: &apiextensionsv1.JSONSchemaPropsOrArray{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "object",
													Properties: map[string]apiextensionsv1.JSONSchemaProps{
														"name": {
															Type: "string",
														},
														"mountPath": {
															Type: "string",
														},
														"size": {
															Type: "string",
														},
														"storageClassName": {
															Type: "string",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// CreateCustomResource creates a custom resource of the specified type
func (r *CRDRegistry) CreateCustomResource(ctx context.Context, dynamicClient dynamic.Interface, kind, name, namespace string, spec map[string]interface{}) error {
	// Determine the GVR based on the Kind
	var resource string
	switch kind {
	case "WorkloadProcess":
		resource = ProcessesResource
	case "WorkloadService":
		resource = ServicesResource
	case "WorkloadCronJob":
		resource = CronjobsResource
	case "WorkloadPersistent":
		resource = PersistentsResource
	default:
		return fmt.Errorf("unsupported workload CRD kind: %s", kind)
	}

	// Create the GroupVersionResource
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: resource,
	}

	// Create the unstructured object
	object := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": fmt.Sprintf("%s/%s", GROUP, MajorVersion),
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}

	// Create the resource
	_, err := dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, object, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Resource already exists, update it
			klog.Infof("Resource %s/%s already exists, updating", namespace, name)
			_, err = dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, object, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update %s '%s/%s': %w", kind, namespace, name, err)
			}
			return nil
		}
		return fmt.Errorf("failed to create %s '%s/%s': %w", kind, namespace, name, err)
	}

	klog.Infof("Successfully created %s '%s/%s'", kind, namespace, name)
	return nil
}
