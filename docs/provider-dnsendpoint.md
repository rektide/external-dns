# dnsendpoint provider for external-dns

## Problem

We want public authoritative DNS for our kubernetes-managed domains, served by CoreDNS from our cluster. The domains include ated.me, istic.me, antifascize.us, phic.us, ered.us, istic.us, ating.us, eldergods.com.

The initial approach tried to use CoreDNS as the cluster DNS resolver (`isClusterService: true`) with the `file` plugin and zone files. This was wrong — we need a **public authoritative DNS server**, not a cluster-internal resolver.

## Architecture investigation

Three integration paths between external-dns and CoreDNS were evaluated:

### Path 1: external-dns → etcd → CoreDNS etcd plugin (rejected)

The standard external-dns `coredns` provider writes DNS records to etcd under `/skydns/`. CoreDNS reads from etcd via its `etcd` plugin. This requires running a dedicated etcd instance or exposing k3s's embedded etcd.

- k3s's embedded etcd listens on `127.0.0.1:2379` and is wrapped by kine — not trivially exposed to pods
- Exposing it would require `hostNetwork: true`, hostPath TLS cert mounts, and control-plane node affinity for external-dns
- A dedicated etcd pod adds ~64MB RAM and another system to manage
- The etcd data path is `external-dns → etcd (JSON service records under /skydns/) → CoreDNS etcd plugin`

### Path 2: CoreDNS k8s_crd plugin + DNSEndpoint CRDs (chosen)

The [coredns-crd-plugin](https://github.com/k8gb-io/coredns-crd-plugin) (`k8s_crd`) watches `DNSEndpoint` CRDs (`externaldns.k8s.io/v1alpha1`) directly via the k8s API. No etcd needed — CoreDNS reads CRDs in real-time.

- Custom CoreDNS image: `ghcr.io/k8gb-io/k8s_crd`
- `isClusterService: false`, `serviceType: LoadBalancer`
- RBAC grants CoreDNS permission to list/watch DNSEndpoint resources
- Zone configuration lists our domains, the `k8s_crd` plugin serves them

### The gap: no provider writes DNSEndpoint CRDs

external-dns has a `crd` **source** that *reads* DNSEndpoint CRDs, but no **provider** that *writes* them. The existing providers write to external services (AWS, Cloudflare) or to etcd (coredns). This is the missing piece.

## Solution: `dnsendpoint` provider

A new external-dns provider that writes `DNSEndpoint` CRDs to the k8s API. This completes the loop:

```
external-dns (dnsendpoint provider) → DNSEndpoint CRDs → CoreDNS (k8s_crd plugin) → serves DNS
```

No etcd anywhere. Both external-dns and CoreDNS talk to the k8s API.

## Implementation

This repo is a fork of `kubernetes-sigs/external-dns` with one addition: `provider/dnsendpoint/`.

### Provider interface

The provider implements `provider.Provider`:

- **`Records()`** — lists all `DNSEndpoint` resources in the configured namespace, extracts their embedded `spec.endpoints`, applies domain filtering
- **`ApplyChanges()`** — groups changes by DNS name, maps DNS names to k8s object names (dots → hyphens), creates/updates/deletes `DNSEndpoint` CRDs accordingly
- **`GetDomainFilter()`** — returns the domain filter for plan evaluation

### Key design decisions

- One `DNSEndpoint` per DNS name (e.g. `foo.example.com` → object `foo-example-com`). This keeps records granular and avoids conflicts when multiple sources produce records for different names.
- Namespace-scoped: reads/writes within `cfg.Namespace` (default `"default"`). Can be overridden with `--namespace` flag.
- Uses `controller-runtime` client for k8s API access (same library as the CRD source)
- Registered as provider `"dnsendpoint"` in the factory map

### Files changed

| File | Change |
|------|--------|
| `provider/dnsendpoint/dnsendpoint.go` | Provider implementation (~155 lines) |
| `provider/dnsendpoint/dnsendpoint_test.go` | Tests using fake k8s client (11 tests) |
| `provider/factory/provider.go` | Register `dnsendpoint` in factory map |
| `pkg/apis/externaldns/constants.go` | Add `ProviderDNSEndpoint = "dnsendpoint"` |

### Helm usage (in future-fuze)

The external-dns helm deployment would set:

```yaml
provider:
  name: dnsendpoint
sources:
  - service
  - ingress
namespace: net-coredns
domainFilters:
  - ated.me
  - istic.me
  - antifascize.us
  - phic.us
  - ered.us
  - istic.us
  - ating.us
  - eldergods.com
```

## Upstream potential

This provider could be proposed as a PR to `kubernetes-sigs/external-dns`. The DNSEndpoint CRD is already defined in that project, the `crd` source already reads them — a provider that writes them is the natural complement. It would enable any k8s-native DNS system (not just CoreDNS with k8s_crd) to consume records via CRDs.

## Future work

- Build and publish a custom external-dns image with this provider
- Create `net/external-dns/helm-external-dns/` in future-fuze
- Consider upstream PR
- Second nameserver (currently only 216.144.229.17)
