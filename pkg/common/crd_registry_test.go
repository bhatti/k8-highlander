package common

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCRDRegistry_RegisterCRDs(t *testing.T) {
	// Create fake apiextensions client
	fakeClient := apiextensionsfake.NewSimpleClientset()
	registry := NewCRDRegistry(fakeClient)

	// Test registering CRDs
	err := registry.RegisterCRDs(context.Background())
	require.NoError(t, err)

	// Verify the CRDs were created
	crd, err := fakeClient.ApiextensionsV1().CustomResourceDefinitions().Get(
		context.Background(),
		"workloadprocesses.highlander.plexobject.io",
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, "WorkloadProcess", crd.Spec.Names.Kind)

	crd, err = fakeClient.ApiextensionsV1().CustomResourceDefinitions().Get(
		context.Background(),
		"workloadservices.highlander.plexobject.io",
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, "WorkloadService", crd.Spec.Names.Kind)

	crd, err = fakeClient.ApiextensionsV1().CustomResourceDefinitions().Get(
		context.Background(),
		"workloadcronjobs.highlander.plexobject.io",
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, "WorkloadCronJob", crd.Spec.Names.Kind)

	crd, err = fakeClient.ApiextensionsV1().CustomResourceDefinitions().Get(
		context.Background(),
		"workloadpersistents.highlander.plexobject.io",
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, "WorkloadPersistent", crd.Spec.Names.Kind)
}

func TestCRDRegistry_EnsureCRD(t *testing.T) {
	// Create fake apiextensions client
	fakeClient := apiextensionsfake.NewSimpleClientset()
	registry := NewCRDRegistry(fakeClient)

	// Create a test CRD
	testCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-crds.highlander.plexobject.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "highlander.plexobject.io",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   "test-crds",
				Singular: "test-crd",
				Kind:     "TestCRD",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"field1": {
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

	// Test creating a new CRD
	err := registry.ensureCRD(context.Background(), testCRD)
	require.NoError(t, err)

	// Verify CRD was created
	crd, err := fakeClient.ApiextensionsV1().CustomResourceDefinitions().Get(
		context.Background(),
		"test-crds.highlander.plexobject.io",
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, "TestCRD", crd.Spec.Names.Kind)

	// Modify the CRD
	testCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["field2"] = apiextensionsv1.JSONSchemaProps{
		Type: "integer",
	}

	// Test updating an existing CRD
	err = registry.ensureCRD(context.Background(), testCRD)
	require.NoError(t, err)

	// Verify CRD was updated
	crd, err = fakeClient.ApiextensionsV1().CustomResourceDefinitions().Get(
		context.Background(),
		"test-crds.highlander.plexobject.io",
		metav1.GetOptions{},
	)
	require.NoError(t, err)

	// Verify the schema was updated
	field2Schema, found := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["field2"]
	assert.True(t, found)
	assert.Equal(t, "integer", field2Schema.Type)
}

// Helper function to create a test dynamic client for custom resources
func NewTestDynamicClient(scheme *runtime.Scheme) dynamic.Interface {
	return dynamicfake.NewSimpleDynamicClient(scheme)
}
