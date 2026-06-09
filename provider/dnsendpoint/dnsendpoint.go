package dnsendpoint

import (
	"context"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	apiv1alpha1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/pkg/apis/externaldns"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

const defaultNamespace = "default"

type dnsEndpointProvider struct {
	provider.BaseProvider
	domainFilter endpoint.DomainFilterInterface
	namespace    string
	k8sClient    client.Client
}

func New(ctx context.Context, cfg *externaldns.Config, domainFilter *endpoint.DomainFilter) (provider.Provider, error) {
	scheme := runtime.NewScheme()
	if err := apiv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding dnsendpoint to scheme: %w", err)
	}

	restCfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("getting k8s config: %w", err)
	}

	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}

	ns := cfg.Namespace
	if ns == "" {
		ns = defaultNamespace
	}

	return &dnsEndpointProvider{
		domainFilter: domainFilter,
		namespace:    ns,
		k8sClient:    k8sClient,
	}, nil
}

func (p *dnsEndpointProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}

func (p *dnsEndpointProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	var list apiv1alpha1.DNSEndpointList
	if err := p.k8sClient.List(ctx, &list, client.InNamespace(p.namespace)); err != nil {
		return nil, fmt.Errorf("listing dnsendpoints: %w", err)
	}

	var endpoints []*endpoint.Endpoint
	for i := range list.Items {
		de := &list.Items[i]
		for _, ep := range de.Spec.Endpoints {
			if !p.domainFilter.Match(ep.DNSName) {
				continue
			}
			ep.Labels[endpoint.ResourceLabelKey] = fmt.Sprintf("dnsendpoint/%s/%s", de.Namespace, de.Name)
			endpoints = append(endpoints, ep)
		}
	}

	return endpoints, nil
}

func (p *dnsEndpointProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	byDNSName := p.groupByDNSName(changes)

	for dnsName, eps := range byDNSName {
		objName := dnsNameToObjectName(dnsName)
		existing := &apiv1alpha1.DNSEndpoint{}

		err := p.k8sClient.Get(ctx, types.NamespacedName{Namespace: p.namespace, Name: objName}, existing)
		if err == nil {
			existing.Spec.Endpoints = eps
			if err := p.k8sClient.Update(ctx, existing); err != nil {
				return fmt.Errorf("updating dnsendpoint %s: %w", objName, err)
			}
			log.Infof("Updated DNSEndpoint %s/%s with %d endpoints", p.namespace, objName, len(eps))
			continue
		}

		obj := &apiv1alpha1.DNSEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      objName,
				Namespace: p.namespace,
			},
			Spec: apiv1alpha1.DNSEndpointSpec{
				Endpoints: eps,
			},
		}
		if err := p.k8sClient.Create(ctx, obj); err != nil {
			return fmt.Errorf("creating dnsendpoint %s: %w", objName, err)
		}
		log.Infof("Created DNSEndpoint %s/%s with %d endpoints", p.namespace, objName, len(eps))
	}

	return nil
}

func (p *dnsEndpointProvider) groupByDNSName(changes *plan.Changes) map[string][]*endpoint.Endpoint {
	result := make(map[string][]*endpoint.Endpoint)

	for _, ep := range changes.Create {
		result[ep.DNSName] = append(result[ep.DNSName], ep)
	}
	for _, ep := range changes.UpdateNew {
		result[ep.DNSName] = append(result[ep.DNSName], ep)
	}

	for _, ep := range changes.Delete {
		objName := dnsNameToObjectName(ep.DNSName)
		obj := &apiv1alpha1.DNSEndpoint{}
		if err := p.k8sClient.Get(context.Background(), types.NamespacedName{Namespace: p.namespace, Name: objName}, obj); err != nil {
			log.Warnf("DNSEndpoint %s not found for deletion: %v", objName, err)
			continue
		}
		if err := p.k8sClient.Delete(context.Background(), obj); err != nil {
			log.Warnf("Failed to delete DNSEndpoint %s: %v", objName, err)
		} else {
			log.Infof("Deleted DNSEndpoint %s/%s", p.namespace, objName)
		}
	}

	return result
}

func dnsNameToObjectName(dnsName string) string {
	name := strings.TrimSuffix(dnsName, ".")
	name = strings.ReplaceAll(name, ".", "-")
	return name
}
