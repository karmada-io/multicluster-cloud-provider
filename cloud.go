package multiclusterprovider

import (
	"context"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

// Interface is an abstract, pluggable interface for multcluster provider.
type Interface interface {
	// LoadBalancer returns a MultiClusterIngress balancer interface, also returns true
	// if the interface is supported, false otherwise.
	LoadBalancer() (LoadBalancer, bool)
	// MCSLoadBalancer returns a MultiClusterService balancer interface, also returns true
	// if the interface is supported, false otherwise.
	MCSLoadBalancer() (MCSLoadBalancer, bool)
	// ProviderName returns the cloud provider ID.
	ProviderName() string
}

// LoadBalancer is an abstract, pluggable interface for MultiClusterIngress load balancers.
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

// MCSLoadBalancer is an abstract, pluggable interface for MultiClusterService load balancers.
type MCSLoadBalancer interface {
	// GetMCSLoadBalancer return whether the specified load balancer exists, and if so, what its status is.
	GetMCSLoadBalancer(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) (status *corev1.LoadBalancerStatus, exist bool, err error)
	// EnsureMCSLoadBalancer creates a new load balancer.
	EnsureMCSLoadBalancer(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) (status *corev1.LoadBalancerStatus, err error)
	// UpdateMCSLoadBalancer updates the specified load balancer.
	UpdateMCSLoadBalancer(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) (status *corev1.LoadBalancerStatus, err error)
	// EnsureMCSLoadBalancerDeleted deletes the specified load balancer if it exists.
	EnsureMCSLoadBalancerDeleted(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) error
}
