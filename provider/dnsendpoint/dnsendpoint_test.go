package dnsendpoint

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1alpha1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = apiv1alpha1.AddToScheme(scheme)
	return scheme
}

func TestDnsNameToObjectName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"foo.example.com", "foo-example-com"},
		{"example.com.", "example-com"},
		{"example.com", "example-com"},
		{"a.b.c.d.example.com", "a-b-c-d-example-com"},
		{"single", "single"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.expected, dnsNameToObjectName(tc.input))
	}
}

func TestRecordsEmpty(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{""}), "test-ns", k8sClient)

	records, err := p.Records(context.Background())
	require.NoError(t, err)
	assert.Empty(t, records)
}

func TestRecordsReturnsEndpoints(t *testing.T) {
	scheme := newTestScheme()
	existing := &apiv1alpha1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "foo-example-com", Namespace: "test-ns"},
		Spec: apiv1alpha1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
				endpoint.NewEndpoint("bar.example.com", endpoint.RecordTypeA, "5.6.7.8"),
			},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{"example.com"}), "test-ns", k8sClient)

	records, err := p.Records(context.Background())
	require.NoError(t, err)
	assert.Len(t, records, 2)

	names := map[string]bool{}
	for _, r := range records {
		names[r.DNSName] = true
		assert.Equal(t, "dnsendpoint/test-ns/foo-example-com", r.Labels[endpoint.ResourceLabelKey])
	}
	assert.True(t, names["foo.example.com"])
	assert.True(t, names["bar.example.com"])
}

func TestRecordsDomainFilter(t *testing.T) {
	scheme := newTestScheme()
	existing := &apiv1alpha1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "mixed", Namespace: "test-ns"},
		Spec: apiv1alpha1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
				endpoint.NewEndpoint("bar.other.com", endpoint.RecordTypeA, "5.6.7.8"),
			},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{"example.com"}), "test-ns", k8sClient)

	records, err := p.Records(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "foo.example.com", records[0].DNSName)
}

func TestApplyChangesCreate(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{""}), "test-ns", k8sClient)

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
		},
	}
	err := p.ApplyChanges(context.Background(), changes)
	require.NoError(t, err)

	var list apiv1alpha1.DNSEndpointList
	require.NoError(t, k8sClient.List(context.Background(), &list, client.InNamespace("test-ns")))
	require.Len(t, list.Items, 1)
	assert.Equal(t, "foo-example-com", list.Items[0].Name)
	require.Len(t, list.Items[0].Spec.Endpoints, 1)
	assert.Equal(t, "foo.example.com", list.Items[0].Spec.Endpoints[0].DNSName)
	assert.Equal(t, "1.2.3.4", list.Items[0].Spec.Endpoints[0].Targets[0])
}

func TestApplyChangesUpdate(t *testing.T) {
	scheme := newTestScheme()
	existing := &apiv1alpha1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "foo-example-com", Namespace: "test-ns"},
		Spec: apiv1alpha1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
			},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{""}), "test-ns", k8sClient)

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{
			endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
		},
		UpdateNew: []*endpoint.Endpoint{
			endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "9.8.7.6"),
		},
	}
	err := p.ApplyChanges(context.Background(), changes)
	require.NoError(t, err)

	updated := &apiv1alpha1.DNSEndpoint{}
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: "foo-example-com"}, updated))
	require.Len(t, updated.Spec.Endpoints, 1)
	assert.Equal(t, "9.8.7.6", updated.Spec.Endpoints[0].Targets[0])
}

func TestApplyChangesDelete(t *testing.T) {
	scheme := newTestScheme()
	existing := &apiv1alpha1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "foo-example-com", Namespace: "test-ns"},
		Spec: apiv1alpha1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
			},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{""}), "test-ns", k8sClient)

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{
			endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
		},
	}
	err := p.ApplyChanges(context.Background(), changes)
	require.NoError(t, err)

	var list apiv1alpha1.DNSEndpointList
	require.NoError(t, k8sClient.List(context.Background(), &list, client.InNamespace("test-ns")))
	assert.Empty(t, list.Items)
}

func TestApplyChangesDeleteNotFound(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{""}), "test-ns", k8sClient)

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{
			endpoint.NewEndpoint("nonexistent.example.com", endpoint.RecordTypeA, "1.2.3.4"),
		},
	}
	err := p.ApplyChanges(context.Background(), changes)
	require.NoError(t, err)
}

func TestApplyChangesMultipleCreates(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{""}), "test-ns", k8sClient)

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpoint("a.example.com", endpoint.RecordTypeA, "1.2.3.4"),
			endpoint.NewEndpoint("b.example.com", endpoint.RecordTypeA, "5.6.7.8"),
		},
	}
	err := p.ApplyChanges(context.Background(), changes)
	require.NoError(t, err)

	var list apiv1alpha1.DNSEndpointList
	require.NoError(t, k8sClient.List(context.Background(), &list, client.InNamespace("test-ns")))
	assert.Len(t, list.Items, 2)

	names := map[string]bool{}
	for _, item := range list.Items {
		names[item.Name] = true
	}
	assert.True(t, names["a-example-com"])
	assert.True(t, names["b-example-com"])
}

func TestApplyChangesCreateThenDelete(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{""}), "test-ns", k8sClient)
	ctx := context.Background()

	err := p.ApplyChanges(ctx, &plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
		},
	})
	require.NoError(t, err)

	err = p.ApplyChanges(ctx, &plan.Changes{
		Delete: []*endpoint.Endpoint{
			endpoint.NewEndpoint("foo.example.com", endpoint.RecordTypeA, "1.2.3.4"),
		},
	})
	require.NoError(t, err)

	var list apiv1alpha1.DNSEndpointList
	require.NoError(t, k8sClient.List(ctx, &list, client.InNamespace("test-ns")))
	assert.Empty(t, list.Items)
}

func TestGetDomainFilter(t *testing.T) {
	df := endpoint.NewDomainFilter([]string{"example.com"})
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	p := newProvider(df, "test-ns", k8sClient)
	assert.Equal(t, df, p.GetDomainFilter())
}

func TestRecordsOnlyFromConfiguredNamespace(t *testing.T) {
	scheme := newTestScheme()
	ns1Endpoint := &apiv1alpha1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"},
		Spec: apiv1alpha1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				endpoint.NewEndpoint("a.example.com", endpoint.RecordTypeA, "1.1.1.1"),
			},
		},
	}
	ns2Endpoint := &apiv1alpha1.DNSEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns2"},
		Spec: apiv1alpha1.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				endpoint.NewEndpoint("b.example.com", endpoint.RecordTypeA, "2.2.2.2"),
			},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(ns1Endpoint, ns2Endpoint).Build()
	p := newProvider(endpoint.NewDomainFilter([]string{"example.com"}), "ns1", k8sClient)

	records, err := p.Records(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "a.example.com", records[0].DNSName)
}
