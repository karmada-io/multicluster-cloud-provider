package main

import (
	"os"

	"k8s.io/component-base/cli"
	cliflag "k8s.io/component-base/cli/flag"
	_ "k8s.io/component-base/logs/json/register" // for JSON log format registration
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	_ "sigs.k8s.io/controller-runtime/pkg/metrics"

	multiclusterprovider "github.com/karmada-io/multicluster-cloud-provider"
	"github.com/karmada-io/multicluster-cloud-provider/app"
	_ "github.com/karmada-io/multicluster-cloud-provider/fake" // import fake test
	"github.com/karmada-io/multicluster-cloud-provider/options"
)

func main() {
	ctx := controllerruntime.SetupSignalHandler()
	opts := options.NewClusterControllerManagerOptions()
	fss := cliflag.NamedFlagSets{}
	cmd := app.NewControllerManagerCommand(ctx, opts, fss, cloudInitializer)

	code := cli.Run(cmd)
	os.Exit(code)
}

func cloudInitializer(name, cloudConfigFile string) multiclusterprovider.Interface {
	provider, err := multiclusterprovider.InitMultiClusterProvider(name, cloudConfigFile)
	if err != nil {
		klog.Fatalf("Multicluster provider could not be initialized: %v", err)
	}
	if provider == nil {
		klog.Fatalf("Multicluster provider is nil")
	}
	return provider
}
