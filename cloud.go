package multiclusterprovider

import (
	"context"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
)

// Interface is an abstract, pluggable interface for multcluster provider.
type Interface interface {
	// LoadBalancer returns a balancer interface, also returns true if the interface is supported, false otherwise.
	LoadBalancer() (LoadBalancer, bool)
	// ProviderName returns the cloud provider ID.
	ProviderName() string
}

// LoadBalancer is an abstract, pluggable interface for load balancers.
type LoadBalancer interface {
	// GetLoadBalancer return whether the specified load balancer exists, and if so, what its status is.
	GetLoadBalancer(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (status *networkingv1.IngressLoadBalancerStatus, exist bool, err error)
	// EnsureLoadBalancer creates a new load balancer.
	EnsureLoadBalancer(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (status *networkingv1.IngressLoadBalancerStatus, err error)
	// UpdateLoadBalancer updates the specified load balancer.
	UpdateLoadBalancer(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (status *networkingv1.IngressLoadBalancerStatus, err error)
	// EnsureLoadBalancerDeleted deletes the specified load balancer if it exists.
	EnsureLoadBalancerDeleted(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) error
}
