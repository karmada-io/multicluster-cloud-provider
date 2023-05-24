package options

import (
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// Validate checks MultiClusterControllerManagerOptions and return a slice of found errs.
func (o *MultiClusterControllerManagerOptions) Validate() field.ErrorList {
	errs := field.ErrorList{}
	newPath := field.NewPath("Options")

	if len(o.Name) == 0 {
		errs = append(errs, field.Invalid(newPath.Child("Name"), o.Name, "--multicluster-provider cannot be empty"))
	}
	if len(o.ProviderClassName) == 0 {
		errs = append(errs, field.Invalid(newPath.Child("ProviderClassName"), o.ProviderClassName, "--provider-ingress-class cannot be empty"))
	}

	return errs
}
