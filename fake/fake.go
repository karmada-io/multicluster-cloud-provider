package fake

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	multiclusterprovider "github.com/karmada-io/multicluster-cloud-provider"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/indexes"
)

const defaultProviderName = "fake"

// Balancer is a fake balancer struct.
type Balancer struct {
	Name       string
	Namespace  string
	MCI        *networkingv1alpha1.MultiClusterIngress
	Services   []string
	Eps        []net.IP
	ExternalIP net.IP
}

// MCSBalancer is a fake balancer struct.
type MCSBalancer struct {
	Name       string
	Namespace  string
	MCS        *networkingv1alpha1.MultiClusterService
	Eps        []net.IP
	ExternalIP net.IP
}

var _ multiclusterprovider.Interface = (*Provider)(nil)
var _ multiclusterprovider.LoadBalancer = (*Provider)(nil)
var _ multiclusterprovider.MCSLoadBalancer = (*Provider)(nil)

// Provider is a fake provider struct.
type Provider struct {
	Name         string
	Balancers    map[string]Balancer
	MCSBalancers map[string]MCSBalancer

	Calls        []string
	RequestDelay time.Duration

	addCallLock sync.Mutex
}

// ProviderName returns the cloud provider name.
func (p *Provider) ProviderName() string {
	if p.Name == "" {
		return defaultProviderName
	}
	return p.Name
}

// LoadBalancer returns a fake implementation of LoadBalancer. Actually it just returns itself.
func (p *Provider) LoadBalancer() (multiclusterprovider.LoadBalancer, bool) {
	return p, true
}

// GetLoadBalancer is a stub implementation of LoadBalancer.GetLoadBalancer.
func (p *Provider) GetLoadBalancer(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (*networkingv1.IngressLoadBalancerStatus, bool, error) {
	p.addCall("get")
	namespacedName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}.String()
	for name := range p.Balancers {
		if name == namespacedName {
			klog.V(4).Infof("can get Balancer with mci %s", namespacedName)
			return &networkingv1.IngressLoadBalancerStatus{}, true, nil
		}
	}

	klog.V(4).Infof("can not get Balancer with mci %s", namespacedName)
	return nil, false, nil
}

// EnsureLoadBalancer is a stub implementation of LoadBalancer.EnsureLoadBalancer.
func (p *Provider) EnsureLoadBalancer(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (status *networkingv1.IngressLoadBalancerStatus, err error) {
	p.addCall("create")
	if p.Balancers == nil {
		p.Balancers = make(map[string]Balancer)
	}

	namespacedName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}.String()
	_, exist := p.Balancers[namespacedName]
	if exist {
		klog.V(4).Infof("there already exist a LoadBalance with mci %s", namespacedName)
		return nil, fmt.Errorf("there already exist a LoadBalancer")
	}

	mciCopy := mci.DeepCopy()
	svcRefs := indexes.BuildServiceRefIndexes(mciCopy)
	p.Balancers[namespacedName] = Balancer{
		Name:      mci.Name,
		Namespace: mci.Namespace,
		MCI:       mciCopy,
		Services:  svcRefs,
	}
	klog.V(4).Infof("success to create a LoadBalance with mci %s", namespacedName)
	return &networkingv1.IngressLoadBalancerStatus{
		Ingress: []networkingv1.IngressLoadBalancerIngress{
			{IP: "1.2.3.4"}, // construct a random IP address and return it.
		},
	}, nil
}

// UpdateLoadBalancer is a stub implementation of LoadBalancer.UpdateLoadBalancer.
func (p *Provider) UpdateLoadBalancer(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (status *networkingv1.IngressLoadBalancerStatus, err error) {
	p.addCall("update")
	namespacedName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}.String()
	lb, exist := p.Balancers[namespacedName]
	if !exist {
		klog.V(4).Infof("there not exist a LoadBalance with mci %s", namespacedName)
		return nil, fmt.Errorf("there not exist a LoadBalancer")
	}

	mciCopy := mci.DeepCopy()
	svcRefs := indexes.BuildServiceRefIndexes(mciCopy)
	lb.MCI = mciCopy
	lb.Services = svcRefs
	klog.V(4).Infof("success to update the LoadBalance with mci %s", namespacedName)
	return &networkingv1.IngressLoadBalancerStatus{
		Ingress: []networkingv1.IngressLoadBalancerIngress{
			{IP: "1.2.3.5"}, // construct a random IP address and return it.
		},
	}, nil
}

// EnsureLoadBalancerDeleted is a stub implementation of LoadBalancer.EnsureLoadBalancerDeleted.
func (p *Provider) EnsureLoadBalancerDeleted(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) error {
	p.addCall("delete")
	namespacedName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}.String()
	delete(p.Balancers, namespacedName)
	klog.V(4).Infof("success to delete the LoadBalance with mci %s", namespacedName)
	return nil
}

// MCSLoadBalancer returns a fake implementation of MCSLoadBalancer. Actually it just returns itself.
func (p *Provider) MCSLoadBalancer() (multiclusterprovider.MCSLoadBalancer, bool) {
	return p, true
}

// GetMCSLoadBalancer is a stub implementation of LoadBalancer.GetMCSLoadBalancer.
func (p *Provider) GetMCSLoadBalancer(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) (status *corev1.LoadBalancerStatus, exist bool, err error) {
	p.addCall("get")
	namespacedName := types.NamespacedName{Namespace: mcs.Namespace, Name: mcs.Name}.String()
	for name := range p.MCSBalancers {
		if name == namespacedName {
			klog.V(4).Infof("can get Balancer with mcs %s", namespacedName)
			return &corev1.LoadBalancerStatus{}, true, nil
		}
	}

	klog.V(4).Infof("can not get Balancer with mcs %s", namespacedName)
	return nil, false, nil
}

// EnsureMCSLoadBalancer is a stub implementation of LoadBalancer.EnsureMCSLoadBalancer.
func (p *Provider) EnsureMCSLoadBalancer(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) (status *corev1.LoadBalancerStatus, err error) {
	p.addCall("create")
	if p.MCSBalancers == nil {
		p.MCSBalancers = make(map[string]MCSBalancer)
	}

	namespacedName := types.NamespacedName{Namespace: mcs.Namespace, Name: mcs.Name}.String()
	_, exist := p.MCSBalancers[namespacedName]
	if exist {
		klog.V(4).Infof("there already exist a LoadBalance with mcs %s", namespacedName)
		return nil, fmt.Errorf("there already exist a LoadBalancer")
	}

	mcsCopy := mcs.DeepCopy()
	p.MCSBalancers[namespacedName] = MCSBalancer{
		Name:      mcs.Name,
		Namespace: mcs.Namespace,
		MCS:       mcsCopy,
	}
	klog.V(4).Infof("success to create a LoadBalance with mcs %s", namespacedName)
	return &corev1.LoadBalancerStatus{
		Ingress: []corev1.LoadBalancerIngress{
			{IP: "1.2.3.4"}, // construct a random IP address and return it.
		},
	}, nil
}

// UpdateMCSLoadBalancer is a stub implementation of LoadBalancer.UpdateMCSLoadBalancer.
func (p *Provider) UpdateMCSLoadBalancer(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) (status *corev1.LoadBalancerStatus, err error) {
	p.addCall("update")
	namespacedName := types.NamespacedName{Namespace: mcs.Namespace, Name: mcs.Name}.String()
	lb, exist := p.MCSBalancers[namespacedName]
	if !exist {
		klog.V(4).Infof("there not exist a LoadBalance with mcs %s", namespacedName)
		return nil, fmt.Errorf("there not exist a LoadBalancer")
	}

	mcsCopy := mcs.DeepCopy()
	lb.MCS = mcsCopy
	klog.V(4).Infof("success to update the LoadBalance with mcs %s", namespacedName)
	return &corev1.LoadBalancerStatus{
		Ingress: []corev1.LoadBalancerIngress{
			{IP: "1.2.3.5"}, // construct a random IP address and return it.
		},
	}, nil
}

// EnsureMCSLoadBalancerDeleted is a stub implementation of LoadBalancer.EnsureMCSLoadBalancerDeleted.
func (p *Provider) EnsureMCSLoadBalancerDeleted(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) error {
	p.addCall("delete")
	namespacedName := types.NamespacedName{Namespace: mcs.Namespace, Name: mcs.Name}.String()
	delete(p.MCSBalancers, namespacedName)
	klog.V(4).Infof("success to delete the LoadBalance with mcs %s", namespacedName)
	return nil
}

func (p *Provider) addCall(desc string) {
	time.Sleep(p.RequestDelay)

	p.addCallLock.Lock()
	defer p.addCallLock.Unlock()
	p.Calls = append(p.Calls, desc)
}

// ClearCalls clears internal record of method calls to this Provider.
func (p *Provider) ClearCalls() {
	p.Calls = []string{}
}
