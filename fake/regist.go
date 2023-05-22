package fake

import (
	"io"

	multiclusterprovider "github.com/karmada-io/multicluster-cloud-provider"
)

func init() {
	multiclusterprovider.RegisterMultiClusterProvider(defaultProviderName, func(config io.Reader) (multiclusterprovider.Interface, error) {
		return &Provider{Name: defaultProviderName}, nil
	})
}
