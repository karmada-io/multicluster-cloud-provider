package multiclusterservice

import (
	"context"
	"reflect"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/genericmanager"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
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
	"github.com/karmada-io/multicluster-cloud-provider/pkg/util"
)

// ControllerName is the controller name that will be used when reporting events.
const ControllerName = "multiclusterservice-controller"

// MCSControllerFinalizer is added to Cluster to ensure MCSLoadBalancer is deleted before itself is deleted.
const MCSControllerFinalizer = "karmada.io/multi-cluster-service-controller"

// LBEventReason is indicates the reason of LB event.
const LBEventReason string = "MCSLoadBalance"

// MCSController is to sync MultiClusterService.
type MCSController struct {
	client.Client
	InformerManager    genericmanager.SingleClusterInformerManager
	MCSLoadBalancer    multiclusterprovider.MCSLoadBalancer
	EventRecorder      record.EventRecorder
	RateLimiterOptions ratelimiterflag.Options
}

// Reconcile performs a full reconciliation for the object referred to by the Request.
// The Controller will requeue the Request to be processed again if an error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (c *MCSController) Reconcile(
	ctx context.Context,
	req controllerruntime.Request,
) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Reconciling MultiClusterService",
		"namespace", req.Namespace, "name", req.Name)

	mcs := &networkingv1alpha1.MultiClusterService{}
	if err := c.Client.Get(ctx, req.NamespacedName, mcs); err != nil {
		if apierrors.IsNotFound(err) {
			// The mcs no longer exist, in which case we stop processing.
			return controllerruntime.Result{}, nil
		}
		klog.ErrorS(err, "failed to get MultiClusterService object",
			"NamespacedName", req.NamespacedName)
		return controllerruntime.Result{}, err
	}

	if !mcs.DeletionTimestamp.IsZero() || !util.MCSContainLoadBalanceType(mcs) {
		return c.handleMCSDelete(ctx, mcs.DeepCopy())
	}
	return c.handleMCSCreateOrUpdate(ctx, mcs.DeepCopy())
}

func (c *MCSController) handleMCSDelete(
	ctx context.Context,
	mcs *networkingv1alpha1.MultiClusterService,
) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Begin to handle MultiClusterService delete event",
		"namespace", mcs.Namespace, "name", mcs.Name)

	if util.MCSContainLoadBalanceType(mcs) {
		err := c.MCSLoadBalancer.EnsureMCSLoadBalancerDeleted(ctx, mcs)
		if err != nil {
			klog.ErrorS(err, "Failed to delete MultiClusterService",
				"namespace", mcs.Namespace, "name", mcs.Name)
			c.EventRecorder.Eventf(mcs, corev1.EventTypeWarning, LBEventReason,
				"Failed to delete MultiClusterService",
				"namespace", mcs.Namespace, "name", mcs.Name, "error", err.Error())
			return controllerruntime.Result{}, err
		}
	} else {
		_, exist, err := c.MCSLoadBalancer.GetMCSLoadBalancer(ctx, mcs)
		if err != nil {
			klog.ErrorS(err, "Failed to get MultiClusterService",
				"namespace", mcs.Namespace, "name", mcs.Name)
			c.EventRecorder.Eventf(mcs, corev1.EventTypeWarning, LBEventReason,
				"Failed to get MultiClusterService",
				"namespace", mcs.Namespace, "name", mcs.Name, "error", err.Error())
			return controllerruntime.Result{}, err
		}

		if exist {
			err := c.MCSLoadBalancer.EnsureMCSLoadBalancerDeleted(ctx, mcs)
			if err != nil {
				klog.ErrorS(err, "Failed to delete MultiClusterService",
					"namespace", mcs.Namespace, "name", mcs.Name)
				c.EventRecorder.Eventf(mcs, corev1.EventTypeWarning, LBEventReason,
					"Failed to delete MultiClusterService",
					"namespace", mcs.Namespace, "name", mcs.Name, "error", err.Error())
				return controllerruntime.Result{}, err
			}
		} else {
			klog.V(4).InfoS("MultiClusterService doesn't contain LoadBalancer type, "+
				"ignore it", "namespace", mcs.Namespace, "name", mcs.Name)
			return controllerruntime.Result{}, nil
		}
	}

	klog.InfoS("Success to delete MultiClusterService",
		"namespace", mcs.Namespace, "name", mcs.Name)

	finalizersUpdated := controllerutil.RemoveFinalizer(mcs, MCSControllerFinalizer)
	if finalizersUpdated {
		err := c.Client.Update(ctx, mcs)
		if err != nil {
			klog.V(4).ErrorS(err, "failed to update MultiClusterService with finalizer",
				"namespace", mcs.Namespace, "name", mcs.Name)
			return controllerruntime.Result{}, err
		}
	}
	return controllerruntime.Result{}, nil
}

func (c *MCSController) handleMCSCreateOrUpdate(
	ctx context.Context,
	mcs *networkingv1alpha1.MultiClusterService,
) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Begin to handle MultiClusterService create or update event",
		"namespace", mcs.Namespace, "name", mcs.Name)

	finalizersUpdated := controllerutil.AddFinalizer(mcs, MCSControllerFinalizer)
	if finalizersUpdated {
		err := c.Client.Update(ctx, mcs)
		if err != nil {
			klog.V(4).ErrorS(err, "failed to update mcs with finalizer",
				"namespace", mcs.Namespace, "name", mcs.Name)
			return controllerruntime.Result{}, err
		}
	}

	_, exist, err := c.MCSLoadBalancer.GetMCSLoadBalancer(ctx, mcs)
	if err != nil {
		klog.ErrorS(err, "failed to get loadBalancer with provider",
			"namespace", mcs.Namespace, "name", mcs.Name)
		return controllerruntime.Result{}, err
	}
	if exist {
		return c.handleMCSUpdate(ctx, mcs)
	}
	return c.handleMCSCreate(ctx, mcs)
}

func (c *MCSController) handleMCSCreate(
	ctx context.Context,
	mcs *networkingv1alpha1.MultiClusterService,
) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Begin to handle MultiClusterService create event",
		"namespace", mcs.Namespace, "name", mcs.Name)

	status, err := c.MCSLoadBalancer.EnsureMCSLoadBalancer(ctx, mcs)
	if err != nil {
		klog.ErrorS(err, "failed to create loadBalancer with provider",
			"namespace", mcs.Namespace, "name", mcs.Name)
		return controllerruntime.Result{}, err
	}
	klog.InfoS("Success to create loadBalancer with provider",
		"namespace", mcs.Namespace, "name", mcs.Name)

	err = c.updateMCSStatus(ctx, mcs, status)
	if err != nil {
		klog.ErrorS(err, "failed to update MultiClusterService status",
			"namespace", mcs.Namespace, "name", mcs.Name)
		return controllerruntime.Result{}, err
	}
	return controllerruntime.Result{}, nil
}

func (c *MCSController) handleMCSUpdate(
	ctx context.Context,
	mcs *networkingv1alpha1.MultiClusterService,
) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Begin to handle MultiClusterService update event",
		"namespace", mcs.Namespace, "name", mcs.Name)

	status, err := c.MCSLoadBalancer.UpdateMCSLoadBalancer(ctx, mcs)
	if err != nil {
		klog.ErrorS(err, "failed to update loadBalancer with provider",
			"namespace", mcs.Namespace, "name", mcs.Name)
		return controllerruntime.Result{}, err
	}
	klog.InfoS("Success to update loadBalancer with provider",
		"namespace", mcs.Namespace, "name", mcs.Name)

	err = c.updateMCSStatus(ctx, mcs, status)
	if err != nil {
		klog.ErrorS(err, "failed to update MultiClusterService status",
			"namespace", mcs.Namespace, "name", mcs.Name)
		return controllerruntime.Result{}, err
	}
	return controllerruntime.Result{}, nil
}

func (c *MCSController) updateMCSStatus(
	ctx context.Context,
	mcs *networkingv1alpha1.MultiClusterService,
	newStatus *corev1.LoadBalancerStatus,
) error {
	if newStatus == nil {
		return nil
	}

	if reflect.DeepEqual(mcs.Status.LoadBalancer, *newStatus) {
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		mcs.Status.LoadBalancer = *newStatus
		updateErr := c.Status().Update(ctx, mcs)
		if updateErr == nil {
			return nil
		}

		updated := &networkingv1alpha1.MultiClusterService{}
		if err = c.Get(ctx, client.ObjectKey{Namespace: mcs.Namespace, Name: mcs.Name}, updated); err == nil {
			mcs = updated
		} else {
			klog.Errorf("Failed to get updated MultiClusterService(%s/%s): %v", mcs.Namespace, mcs.Name, err)
		}
		return updateErr
	})
}

// SetupWithManager creates a controller and register to controller manager.
func (c *MCSController) SetupWithManager(_ context.Context, mgr controllerruntime.Manager) error {
	mcsController, err := controller.New(ControllerName, mgr,
		controller.Options{
			Reconciler:  c,
			RateLimiter: ratelimiterflag.DefaultControllerRateLimiter(c.RateLimiterOptions),
		})
	if err != nil {
		return err
	}

	if err = c.setupWatches(mcsController); err != nil {
		return err
	}

	return nil
}

func (c *MCSController) setupWatches(mcsController controller.Controller) error {
	mcsEventChan := make(chan event.GenericEvent)
	svcEventChan := make(chan event.GenericEvent)

	mcsEventHandler := newMultiClusterServiceEventHandler()
	svcEventHandler := newServiceEventHandler(mcsEventChan, c.Client)
	epsEventHandler := newEndpointSlicesEventHandler(svcEventChan)

	if err := mcsController.Watch(&source.Kind{Type: &networkingv1alpha1.MultiClusterService{}}, mcsEventHandler); err != nil {
		return err
	}
	if err := mcsController.Watch(&source.Channel{Source: mcsEventChan}, mcsEventHandler); err != nil {
		return err
	}
	if err := mcsController.Watch(&source.Kind{Type: &corev1.Service{}}, svcEventHandler); err != nil {
		return err
	}
	if err := mcsController.Watch(&source.Channel{Source: svcEventChan}, svcEventHandler); err != nil {
		return err
	}
	if err := mcsController.Watch(&source.Kind{Type: &discoveryv1.EndpointSlice{}}, epsEventHandler); err != nil {
		return err
	}
	return nil
}
