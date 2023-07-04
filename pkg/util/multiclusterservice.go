package util

import networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"

// MCSContainLoadBalanceType checks weather the MultiClusterService contains LoadBalancer type.
func MCSContainLoadBalanceType(mcs *networkingv1alpha1.MultiClusterService) bool {
	for _, t := range mcs.Spec.Types {
		if t == networkingv1alpha1.ExposureTypeLoadBalancer {
			return true
		}
	}
	return false
}
