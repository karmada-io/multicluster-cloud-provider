package util

import (
	"context"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	"github.com/pkg/errors"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetDefaultIngressClass returns the default IngressClass form the list of IngressClasses.
// If multiple IngressClasses are marked as the default, it returns an error.
// If no IngressClass is marked as the default, it returns an empty string.
func GetDefaultIngressClass(ctx context.Context, c client.Client) (string, error) {
	var defaultClass string
	var defaultClassFound bool

	ingClassList := &networkingv1.IngressClassList{}
	if err := c.List(ctx, ingClassList); err != nil {
		return "", err
	}

	for _, ingClass := range ingClassList.Items {
		if ingClass.Annotations[networkingv1.AnnotationIsDefaultIngressClass] != "true" {
			continue
		}
		if defaultClassFound {
			return "", errors.Errorf("multiple default IngressClasses found")
		}
		defaultClass = ingClass.Name
		defaultClassFound = true
	}
	return defaultClass, nil
}

// CheckIngressClassMatched checks weather the .spec.controller value of the IngressClass
// referenced in input mci Object matched the input ingClassName value.
func CheckIngressClassMatched(ctx context.Context, c client.Client, mci *networkingv1alpha1.MultiClusterIngress, ingClassName string) bool {
	mciIngressClassName := mci.Spec.IngressClassName

	if mciIngressClassName == nil {
		defaultIngressClassName, err := GetDefaultIngressClass(ctx, c)
		if err != nil {
			klog.ErrorS(err, "failed to get default IngressClass")
			return false
		}
		if defaultIngressClassName == "" {
			return false
		}
		mciIngressClassName = &defaultIngressClassName
	}

	ingClassKey := types.NamespacedName{Name: *mciIngressClassName}
	ingClass := &networkingv1.IngressClass{}
	if err := c.Get(ctx, ingClassKey, ingClass); err != nil {
		klog.Errorf("Failed to get target IngressClass %s", ingClassKey.String())
		return false
	}

	return ingClass.Spec.Controller == ingClassName
}
