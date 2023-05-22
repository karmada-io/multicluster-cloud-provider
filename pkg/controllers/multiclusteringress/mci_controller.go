package multiclusteringress

import (
	"context"
	"reflect"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/genericmanager"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/source"

	multiclusterprovider "github.com/karmada-io/multicluster-cloud-provider"
)

// ControllerName is the controller name that will be used when reporting events.
const ControllerName = "multiclusteringress-controller"

// MCIControllerFinalizer is added to Cluster to ensure LoadBalancer is deleted before itself is deleted.
const MCIControllerFinalizer = "karmada.io/multi-cluster-ingress-controller"

// MCIController is to sync MultiClusterIngress.
type MCIController struct {
	client.Client
	InformerManager    genericmanager.SingleClusterInformerManager
	LoadBalancer       multiclusterprovider.LoadBalancer
	EventRecorder      record.EventRecorder
	RateLimiterOptions ratelimiterflag.Options
	ProviderClassName  string
}

// Reconcile performs a full reconciliation for the object referred to by the Request.
// The Controller will requeue the Request to be processed again if an error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (c *MCIController) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Reconciling MultiClusterIngress", "namespace", req.Namespace, "name", req.Name)

	mci := &networkingv1alpha1.MultiClusterIngress{}
	if err := c.Client.Get(ctx, req.NamespacedName, mci); err != nil {
		if apierrors.IsNotFound(err) {
			// The mci no longer exist, in which case we stop processing.
			return controllerruntime.Result{}, nil
		}
		klog.ErrorS(err, "failed to get multiClusterIngress object", "NamespacedName", req.NamespacedName)
		return controllerruntime.Result{}, err
	}

	if !mci.DeletionTimestamp.IsZero() {
		return c.handleMCIDelete(ctx, mci.DeepCopy())
	}
	return c.handleMCICreateOrUpdate(ctx, mci.DeepCopy())
}

func (c *MCIController) handleMCIDelete(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Begin to handle multiClusterIngress delete event", "namespace", mci.Namespace, "name", mci.Name)

	err := c.LoadBalancer.EnsureLoadBalancerDeleted(ctx, mci)
	if err != nil {
		klog.ErrorS(err, "Failed to delete multiClusterIngress", "namespace", mci.Namespace, "name", mci.Name)
		return controllerruntime.Result{}, err
	}
	klog.InfoS("Success to delete multiClusterIngress", "namespace", mci.Namespace, "name", mci.Name)

	finalizersUpdated := controllerutil.RemoveFinalizer(mci, MCIControllerFinalizer)
	if finalizersUpdated {
		err = c.Client.Update(ctx, mci)
		if err != nil {
			klog.V(4).ErrorS(err, "failed to update mci with finalizer", "namespace", mci.Namespace, "name", mci.Name)
			return controllerruntime.Result{}, err
		}
	}
	return controllerruntime.Result{}, nil
}

func (c *MCIController) handleMCICreateOrUpdate(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Begin to handle multiClusterIngress create or update event", "namespace", mci.Namespace, "name", mci.Name)

	finalizersUpdated := controllerutil.AddFinalizer(mci, MCIControllerFinalizer)
	if finalizersUpdated {
		err := c.Client.Update(ctx, mci)
		if err != nil {
			klog.V(4).ErrorS(err, "failed to update mci with finalizer", "namespace", mci.Namespace, "name", mci.Name)
			return controllerruntime.Result{}, err
		}
	}

	_, exist, err := c.LoadBalancer.GetLoadBalancer(ctx, mci)
	if err != nil {
		klog.ErrorS(err, "failed to get loadBalancer with provider", "namespace", mci.Namespace, "name", mci.Name)
		return controllerruntime.Result{}, err
	}
	if exist {
		return c.handleMCIUpdate(ctx, mci)
	}
	return c.handleMCICreate(ctx, mci)
}

func (c *MCIController) handleMCICreate(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Begin to handle multiClusterIngress create event", "namespace", mci.Namespace, "name", mci.Name)

	status, err := c.LoadBalancer.EnsureLoadBalancer(ctx, mci)
	if err != nil {
		klog.ErrorS(err, "failed to create loadBalancer with provider", "namespace", mci.Namespace, "name", mci.Name)
		return controllerruntime.Result{}, err
	}
	klog.InfoS("Success to create loadBalancer with provider", "namespace", mci.Namespace, "name", mci.Name)

	err = c.updateMCIStatus(ctx, mci, status)
	if err != nil {
		klog.ErrorS(err, "failed to update mci status", "namespace", mci.Namespace, "name", mci.Name)
		return controllerruntime.Result{}, err
	}
	return controllerruntime.Result{}, nil
}

func (c *MCIController) handleMCIUpdate(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Begin to handle multiClusterIngress update event", "namespace", mci.Namespace, "name", mci.Name)

	status, err := c.LoadBalancer.UpdateLoadBalancer(ctx, mci)
	if err != nil {
		klog.ErrorS(err, "failed to update loadBalancer with provider", "namespace", mci.Namespace, "name", mci.Name)
		return controllerruntime.Result{}, err
	}
	klog.InfoS("Success to update loadBalancer with provider", "namespace", mci.Namespace, "name", mci.Name)

	err = c.updateMCIStatus(ctx, mci, status)
	if err != nil {
		klog.ErrorS(err, "failed to update mci status", "namespace", mci.Namespace, "name", mci.Name)
		return controllerruntime.Result{}, err
	}
	return controllerruntime.Result{}, nil
}

func (c *MCIController) updateMCIStatus(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress, newStatus *networkingv1.IngressLoadBalancerStatus) error {
	if reflect.DeepEqual(mci.Status.LoadBalancer, *newStatus) {
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		mci.Status.LoadBalancer = *newStatus
		updateErr := c.Status().Update(ctx, mci)
		if updateErr == nil {
			return nil
		}

		updated := &networkingv1alpha1.MultiClusterIngress{}
		if err = c.Get(ctx, client.ObjectKey{Namespace: mci.Namespace, Name: mci.Name}, updated); err != nil {
			mci = updated
		} else {
			klog.Errorf("Failed to get updated multiClusterIngress(%s/%s): %v", mci.Namespace, mci.Name, err)
		}
		return updateErr
	})
}

// SetupWithManager creates a controller and register to controller manager.
func (c *MCIController) SetupWithManager(ctx context.Context, mgr controllerruntime.Manager) error {
	mciController, err := controller.New(ControllerName, mgr,
		controller.Options{
			Reconciler:  c,
			RateLimiter: ratelimiterflag.DefaultControllerRateLimiter(c.RateLimiterOptions),
		})
	if err != nil {
		return err
	}

	if err = c.setupWatches(ctx, mciController); err != nil {
		return err
	}

	return nil
}

func (c *MCIController) setupWatches(ctx context.Context, mciController controller.Controller) error {
	ingEventChan := make(chan event.GenericEvent)
	svcEventChan := make(chan event.GenericEvent)

	ingEventHandler := newMultiClusterIngressEventHandler(ctx, c.Client, c.ProviderClassName)
	svcEventHandler := newServiceEventHandler(ingEventChan, c.Client)
	epsEventHandler := newEndpointSlicesEventHandler(svcEventChan)

	if err := mciController.Watch(&source.Kind{Type: &networkingv1alpha1.MultiClusterIngress{}}, ingEventHandler); err != nil {
		return err
	}
	if err := mciController.Watch(&source.Channel{Source: ingEventChan}, ingEventHandler); err != nil {
		return err
	}
	if err := mciController.Watch(&source.Kind{Type: &corev1.Service{}}, svcEventHandler); err != nil {
		return err
	}
	if err := mciController.Watch(&source.Channel{Source: svcEventChan}, svcEventHandler); err != nil {
		return err
	}
	if err := mciController.Watch(&source.Kind{Type: &discoveryv1.EndpointSlice{}}, epsEventHandler); err != nil {
		return err
	}
	return nil
}
