package fake

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
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

var _ multiclusterprovider.Interface = (*Provider)(nil)
var _ multiclusterprovider.LoadBalancer = (*Provider)(nil)

// Provider is a fake provider struct.
type Provider struct {
	Name      string
	Balancers map[string]Balancer

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
	namespaceName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}.String()
	for name := range p.Balancers {
		if name == namespaceName {
			klog.V(4).Infof("can get Balancer with mci %s", namespaceName)
			return &networkingv1.IngressLoadBalancerStatus{}, true, nil
		}
	}

	klog.V(4).Infof("can not get Balancer with mci %s", namespaceName)
	return nil, false, nil
}

// EnsureLoadBalancer is a stub implementation of LoadBalancer.EnsureLoadBalancer.
func (p *Provider) EnsureLoadBalancer(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (status *networkingv1.IngressLoadBalancerStatus, err error) {
	p.addCall("create")
	if p.Balancers == nil {
		p.Balancers = make(map[string]Balancer)
	}

	namespaceName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}.String()
	_, exist := p.Balancers[namespaceName]
	if exist {
		klog.V(4).Infof("there already exist a LoadBalance with mci %s", namespaceName)
		return nil, fmt.Errorf("there already exist a LoadBalancer")
	}

	mciCopy := mci.DeepCopy()
	svcRefs := indexes.BuildServiceRefIndexes(mciCopy)
	p.Balancers[namespaceName] = Balancer{
		Name:      mci.Name,
		Namespace: mci.Namespace,
		MCI:       mciCopy,
		Services:  svcRefs,
	}
	klog.V(4).Infof("success to create a LoadBalance with mci %s", namespaceName)
	return &networkingv1.IngressLoadBalancerStatus{
		Ingress: []networkingv1.IngressLoadBalancerIngress{
			{IP: "1.2.3.4"}, // construct a random IP address and return it.
		},
	}, nil
}

// UpdateLoadBalancer is a stub implementation of LoadBalancer.UpdateLoadBalancer.
func (p *Provider) UpdateLoadBalancer(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (status *networkingv1.IngressLoadBalancerStatus, err error) {
	p.addCall("update")
	namespaceName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}.String()
	lb, exist := p.Balancers[namespaceName]
	if !exist {
		klog.V(4).Infof("there not exist a LoadBalance with mci %s", namespaceName)
		return nil, fmt.Errorf("there not exist a LoadBalancer")
	}

	mciCopy := mci.DeepCopy()
	svcRefs := indexes.BuildServiceRefIndexes(mciCopy)
	lb.MCI = mciCopy
	lb.Services = svcRefs
	klog.V(4).Infof("success to update the LoadBalance with mci %s", namespaceName)
	return &networkingv1.IngressLoadBalancerStatus{
		Ingress: []networkingv1.IngressLoadBalancerIngress{
			{IP: "1.2.3.5"}, // construct a random IP address and return it.
		},
	}, nil
}

// EnsureLoadBalancerDeleted is a stub implementation of LoadBalancer.EnsureLoadBalancerDeleted.
func (p *Provider) EnsureLoadBalancerDeleted(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) error {
	p.addCall("delete")
	namespaceName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}.String()
	delete(p.Balancers, namespaceName)
	klog.V(4).Infof("success to delete the LoadBalance with mci %s", namespaceName)
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
