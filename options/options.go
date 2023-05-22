package options

import (
	"time"

	"github.com/karmada-io/karmada/pkg/sharedcli/profileflag"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	componentbaseconfig "k8s.io/component-base/config"
)

const (
	defaultBindAddress = "0.0.0.0"
	defaultPort        = 10368
)

const (
	// NamespaceKarmadaSystem is the karmada system namespace.
	NamespaceKarmadaSystem = "karmada-system"
)

var (
	defaultElectionLeaseDuration = metav1.Duration{Duration: 15 * time.Second}
	defaultElectionRenewDeadline = metav1.Duration{Duration: 10 * time.Second}
	defaultElectionRetryPeriod   = metav1.Duration{Duration: 2 * time.Second}
)

// MultiClusterControllerManagerOptions contains everything necessary to create and run multicluster-provider.
type MultiClusterControllerManagerOptions struct {
	// Name is the provider for multicluster services.
	Name string
	// CloudConfigFile is the path to the multicluster provider configuration file.
	CloudConfigFile string
	// ProviderClassName is the name of the ingress class this provider satisfies.
	ProviderClassName string
	// LeaderElection defines the configuration of leader election client.
	LeaderElection componentbaseconfig.LeaderElectionConfiguration
	// BindAddress is the IP address on which to listen for the --secure-port port.
	BindAddress string
	// SecurePort is the port that the server serves at.
	// Note: We hope support https in the future once controller-runtime provides the functionality.
	SecurePort int
	// KubeAPIQPS is the QPS to use while talking with karmada-apiserver.
	KubeAPIQPS float32
	// KubeAPIBurst is the burst to allow while talking with karmada-apiserver.
	KubeAPIBurst int
	// ResyncPeriod is the base frequency the informers are resynced.
	// Defaults to 0, which means the created informer will never do resyncs.
	ResyncPeriod metav1.Duration
	// MetricsBindAddress is the TCP address that the controller should bind to
	// for serving prometheus metrics.
	// It can be set to "0" to disable the metrics serving.
	// Defaults to ":8080".
	MetricsBindAddress string

	RateLimiterOpts ratelimiterflag.Options
	ProfileOpts     profileflag.Options
}

// NewClusterControllerManagerOptions builds an empty MultiClusterControllerManagerOptions.
func NewClusterControllerManagerOptions() *MultiClusterControllerManagerOptions {
	return &MultiClusterControllerManagerOptions{
		LeaderElection: componentbaseconfig.LeaderElectionConfiguration{
			LeaderElect:       true,
			ResourceLock:      resourcelock.LeasesResourceLock,
			ResourceNamespace: NamespaceKarmadaSystem,
			ResourceName:      "multicluster-controller-manager",
		},
	}
}

// AddFlags adds flags to the specified FlagSet.
func (o *MultiClusterControllerManagerOptions) AddFlags(flags *pflag.FlagSet) {
	flags.StringVar(&o.Name, "multicluster-provider", o.Name,
		"The provider for multicluster services. Empty for no provider.")
	flags.StringVar(&o.CloudConfigFile, "cloud-config", o.CloudConfigFile,
		"The path to the multicluster provider configuration file. Empty for no configuration file.")
	flags.StringVar(&o.ProviderClassName, "provider-ingress-class", o.ProviderClassName,
		"The name of the ingress class this provider satisfies. The .spec.controller value of the IngressClass "+
			"referenced in an MultiClusterIngress Object should be the same value specified here.")

	flags.BoolVar(&o.LeaderElection.LeaderElect, "leader-elect", true, "Start a leader election "+
		"client and gain leadership before executing the main loop. Enable this when running replicated components "+
		"for high availability.")
	flags.StringVar(&o.LeaderElection.ResourceNamespace, "leader-elect-resource-namespace", NamespaceKarmadaSystem,
		"The namespace of resource object that is used for locking during leader election.")
	flags.DurationVar(&o.LeaderElection.LeaseDuration.Duration, "leader-elect-lease-duration",
		defaultElectionLeaseDuration.Duration, ""+
			"The duration that non-leader candidates will wait after observing a leadership "+
			"renewal until attempting to acquire leadership of a led but unrenewed leader "+
			"slot. This is effectively the maximum duration that a leader can be stopped "+
			"before it is replaced by another candidate. This is only applicable if leader "+
			"election is enabled.")
	flags.DurationVar(&o.LeaderElection.RenewDeadline.Duration, "leader-elect-renew-deadline",
		defaultElectionRenewDeadline.Duration, ""+
			"The interval between attempts by the acting master to renew a leadership slot "+
			"before it stops leading. This must be less than or equal to the lease duration. "+
			"This is only applicable if leader election is enabled.")
	flags.DurationVar(&o.LeaderElection.RetryPeriod.Duration, "leader-elect-retry-period",
		defaultElectionRetryPeriod.Duration, ""+
			"The duration the clients should wait between attempting acquisition and renewal "+
			"of a leadership. This is only applicable if leader election is enabled.")

	flags.StringVar(&o.BindAddress, "bind-address", defaultBindAddress, "The IP address on which to listen "+
		"for the --secure-port port.")
	flags.IntVar(&o.SecurePort, "secure-port", defaultPort, "The secure port on which to serve HTTPS.")
	flags.Float32Var(&o.KubeAPIQPS, "kube-api-qps", 40.0, "QPS to use while talking with "+
		"karmada-apiserver. Doesn't cover events and node heartbeat apis which rate limiting is controlled by "+
		"a different set of flags.")
	flags.IntVar(&o.KubeAPIBurst, "kube-api-burst", 60, "Burst to use while talking with "+
		"karmada-apiserver. Doesn't cover events and node heartbeat apis which rate limiting is controlled by "+
		"a different set of flags.")
	flags.DurationVar(&o.ResyncPeriod.Duration, "resync-period", 0,
		"Base frequency the informers are resynced.")
	flags.StringVar(&o.MetricsBindAddress, "metrics-bind-address", ":8080",
		"The TCP address that the controller should bind to for serving prometheus "+
			"metrics(e.g. 127.0.0.1:8080, :8080). It can be set to \"0\" to disable the metrics serving.")

	o.RateLimiterOpts.AddFlags(flags)
	o.ProfileOpts.AddFlags(flags)
}
