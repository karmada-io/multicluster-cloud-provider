package multiclusterprovider

import (
	"fmt"
	"io"
	"os"
	"sync"

	"k8s.io/klog/v2"
)

// Factory is a function that returns a multiclusterprovider.Interface.
// The config parameter provides an io.Reader handler to the factory in
// order to load specific configurations. If no configuration is provided
// the parameter is nil.
type Factory func(config io.Reader) (Interface, error)

var (
	providersMutex sync.Mutex
	providers      = make(map[string]Factory)
)

// RegisterMultiClusterProvider registers a multiclusterprovider.Factory by name.
// This is expected to happen during app startup.
func RegisterMultiClusterProvider(name string, cloudFactory Factory) {
	providersMutex.Lock()
	defer providersMutex.Unlock()

	if _, found := providers[name]; found {
		klog.Fatalf("Multicluster provider %q was registered twice", name)
	}
	klog.V(1).Infof("Registered multicluster provider %q", name)
	providers[name] = cloudFactory
}

// InitMultiClusterProvider creates an instance of the named multicluster provider.
func InitMultiClusterProvider(name string, configFilePath string) (Interface, error) {
	var provider Interface
	var err error

	if name == "" {
		return nil, nil
	}

	if configFilePath == "" {
		provider, err = createMultiClusterProvider(name, nil)
	} else {
		var config *os.File
		config, err = os.Open(configFilePath)
		if err != nil {
			klog.Fatalf("Couldn't open multicluster provider configuration %q: %#v", configFilePath, err)
		}

		defer func() {
			_ = config.Close()
		}()
		provider, err = createMultiClusterProvider(name, config)
	}

	if err != nil {
		return nil, fmt.Errorf("could not init multicluster provider %q: %s", name, err)
	}
	if provider == nil {
		return nil, fmt.Errorf("unknown multicluster provider %q", name)
	}
	return provider, nil
}

// createMultiClusterProvider creates an instance of the named multicluster provider,
// or nil if the name is unknown. The return error is only used if the named provider
// was known but failed to initialize. The config parameter specifies the io.Reader
// handler of the configuration file for the multicluster provider, or ni for no configuration.
func createMultiClusterProvider(name string, config io.Reader) (Interface, error) {
	providersMutex.Lock()
	defer providersMutex.Unlock()

	f, found := providers[name]
	if !found {
		return nil, nil
	}
	return f(config)
}
