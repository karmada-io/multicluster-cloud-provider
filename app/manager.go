package app

import (
	"context"
	"flag"
	"net"
	"strconv"

	"github.com/karmada-io/karmada/pkg/sharedcli"
	"github.com/karmada-io/karmada/pkg/sharedcli/klogflag"
	"github.com/karmada-io/karmada/pkg/sharedcli/profileflag"
	"github.com/karmada-io/karmada/pkg/util/fedinformer"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/genericmanager"
	"github.com/karmada-io/karmada/pkg/util/gclient"
	"github.com/karmada-io/karmada/pkg/util/restmapper"
	"github.com/karmada-io/karmada/pkg/version"
	"github.com/karmada-io/karmada/pkg/version/sharedcommand"
	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/term"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	multiclusterprovider "github.com/karmada-io/multicluster-cloud-provider"
	"github.com/karmada-io/multicluster-cloud-provider/options"
	controllersctx "github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/context"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/indexes"
)

var controllers = make(controllersctx.Initializers)

func init() {
	controllers["multiclusteringress"] = startMCIController
	controllers["multiclusterservice"] = startMCSController
	controllers["crd-installation"] = startCRDInstallationController
	controllers["serviceexport-propagation"] = startServiceExportPropagationController
	controllers["mci-service-locations"] = startMCIServiceLocationsController
}

// InitProviderFunc is used to initialize multicluster provider
type InitProviderFunc func(name, cloudConfigFile string) multiclusterprovider.Interface

// NewControllerManagerCommand creates a *cobra.Command object with default parameters
func NewControllerManagerCommand(ctx context.Context,
	opts *options.MultiClusterControllerManagerOptions,
	additionalFlags cliflag.NamedFlagSets,
	providerInitializer InitProviderFunc,
) *cobra.Command {
	cmd := &cobra.Command{
		Use: "multicluster-controller-manager",
		Long: `The MultiCluster controller manager is a daemon that embeds
the cloud specific control loops shipped with Karmada.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			// validate options
			if errs := opts.Validate(); len(errs) != 0 {
				return errs.ToAggregate()
			}

			provider := providerInitializer(opts.Name, opts.CloudConfigFile)
			return Run(ctx, opts, provider)
		},
	}

	fss := cliflag.NamedFlagSets{}

	genericFlagSet := fss.FlagSet("generic")
	// Add the flag(--kubeconfig) that is added by controller-runtime
	// (https://github.com/kubernetes-sigs/controller-runtime/blob/v0.11.1/pkg/client/config/config.go#L39),
	// and update the flag usage.
	genericFlagSet.AddGoFlagSet(flag.CommandLine)
	genericFlagSet.Lookup("kubeconfig").Usage = "Path to karmada control plane kubeconfig file."
	opts.AddFlags(genericFlagSet)

	// Set klog flags
	logsFlagSet := fss.FlagSet("logs")
	klogflag.Add(logsFlagSet)

	cmd.AddCommand(sharedcommand.NewCmdVersion("multicluster-controller-manager"))
	cmd.Flags().AddFlagSet(genericFlagSet)
	cmd.Flags().AddFlagSet(logsFlagSet)
	for _, f := range additionalFlags.FlagSets {
		cmd.Flags().AddFlagSet(f)
	}

	cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	sharedcli.SetUsageAndHelpFunc(cmd, fss, cols)
	return cmd
}

// Run runs the controller-manager with options. This should never exit.
func Run(ctx context.Context, opts *options.MultiClusterControllerManagerOptions, cloudProvider multiclusterprovider.Interface) error {
	klog.Infof("multicluster-controller-manager version: %s", version.Get())

	profileflag.ListenAndServe(opts.ProfileOpts)

	config, err := controllerruntime.GetConfig()
	if err != nil {
		panic(err)
	}
	config.QPS, config.Burst = opts.KubeAPIQPS, opts.KubeAPIBurst
	controllerManager, err := controllerruntime.NewManager(config, controllerruntime.Options{
		Logger:                     klog.Background(),
		Scheme:                     gclient.NewSchema(),
		Cache:                      cache.Options{SyncPeriod: &opts.ResyncPeriod.Duration},
		LeaderElection:             opts.LeaderElection.LeaderElect,
		LeaderElectionID:           opts.LeaderElection.ResourceName,
		LeaderElectionNamespace:    opts.LeaderElection.ResourceNamespace,
		LeaseDuration:              &opts.LeaderElection.LeaseDuration.Duration,
		RenewDeadline:              &opts.LeaderElection.RenewDeadline.Duration,
		RetryPeriod:                &opts.LeaderElection.RetryPeriod.Duration,
		LeaderElectionResourceLock: opts.LeaderElection.ResourceLock,
		HealthProbeBindAddress:     net.JoinHostPort(opts.BindAddress, strconv.Itoa(opts.SecurePort)),
		LivenessEndpointName:       "/healthz",
		Metrics:                    metricsserver.Options{BindAddress: opts.MetricsBindAddress},
		MapperProvider:             restmapper.MapperProvider,
		BaseContext: func() context.Context {
			return ctx
		},
		NewCache: func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
			opts.DefaultTransform = fedinformer.StripUnusedFields
			return cache.New(config, opts)
		},
	})
	if err != nil {
		klog.Errorf("Failed to build controller manager: %v", err)
		return err
	}

	if err := controllerManager.AddHealthzCheck("ping", healthz.Ping); err != nil {
		klog.Errorf("Failed to add health check endpoint: %v", err)
		return err
	}

	setupControllers(ctx, controllerManager, cloudProvider, opts)

	// blocks until the context is done.
	if err := controllerManager.Start(ctx); err != nil {
		klog.Errorf("controller manager exits unexpectedly: %v", err)
		return err
	}

	// never reach here
	return nil
}

// setupControllers initialize controllers and setup one by one.
func setupControllers(ctx context.Context, mgr controllerruntime.Manager, cloudProvider multiclusterprovider.Interface, opts *options.MultiClusterControllerManagerOptions) {
	restConfig := mgr.GetConfig()
	dynamicClientSet := dynamic.NewForConfigOrDie(restConfig)

	controlPlaneInformerManager := genericmanager.NewSingleClusterInformerManager(ctx, dynamicClientSet, 0)

	setupIndexesForMCI(ctx, mgr.GetFieldIndexer())

	controllerCtx := controllersctx.Context{
		Context:       ctx,
		Mgr:           mgr,
		CloudProvider: cloudProvider,
		Opts: controllersctx.Options{
			Controllers:        controllers.ControllerNames(),
			RateLimiterOptions: opts.RateLimiterOpts,
		},
		DynamicClientSet:  dynamicClientSet,
		InformerManager:   controlPlaneInformerManager,
		ProviderClassName: opts.ProviderClassName,
	}
	if err := controllers.StartControllers(controllerCtx, nil); err != nil {
		klog.Fatalf("error starting controllers: %v", err)
	}

	// Ensure the InformerManager stops when the stop channel closes
	go func() {
		<-ctx.Done()
		genericmanager.StopInstance()
	}()
}

func setupIndexesForMCI(ctx context.Context, fieldIndexer client.FieldIndexer) {
	if err := indexes.SetupServiceIndexesForMCI(ctx, fieldIndexer); err != nil {
		klog.Fatalf("failed to setup service indexes for MultiClusterIngress object: %v", err)
	}

	if err := indexes.SetupSecretIndexesForMCI(ctx, fieldIndexer); err != nil {
		klog.Fatalf("failed to setup secret indexes for MultiClusterIngress object: %v", err)
	}
}
