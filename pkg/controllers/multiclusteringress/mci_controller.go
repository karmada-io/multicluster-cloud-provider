package multiclusteringress

import (
	"context"
	"reflect"
	"sort"

	clusterv1alpha1 "github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	remedyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/remedy/v1alpha1"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/genericmanager"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
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

	if !controllerutil.ContainsFinalizer(mci, MCIControllerFinalizer) {
		objPatch := client.MergeFrom(mci)
		modifiedObj := mci.DeepCopy()
		controllerutil.AddFinalizer(modifiedObj, MCIControllerFinalizer)
		err := c.Client.Patch(ctx, modifiedObj, objPatch)
		if err != nil {
			klog.V(4).ErrorS(err, "failed to update mci with finalizer", "namespace", mci.Namespace, "name", mci.Name)
			return controllerruntime.Result{}, err
		}
	}

	if err := c.updateMCITrafficBlockClusters(ctx, mci); err != nil {
		return controllerruntime.Result{}, err
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

func (c *MCIController) updateMCITrafficBlockClusters(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) error {
	locatedClusters := sets.NewString()
	for _, location := range mci.Status.ServiceLocations {
		locatedClusters.Insert(location.Clusters...)
	}

	clusterList := &clusterv1alpha1.ClusterList{}
	if err := c.Client.List(ctx, clusterList); err != nil {
		klog.Errorf("Failed to list cluster: %v", err)
		return err
	}

	var trafficBlockClusters []string
	for _, cluster := range clusterList.Items {
		if !locatedClusters.Has(cluster.Name) {
			continue
		}
		for _, action := range cluster.Status.RemedyActions {
			if action == string(remedyv1alpha1.TrafficControl) {
				trafficBlockClusters = append(trafficBlockClusters, cluster.Name)
				break
			}
		}
	}
	sort.Strings(trafficBlockClusters)

	mciNamespacedName := types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if reflect.DeepEqual(trafficBlockClusters, mci.Status.TrafficBlockClusters) {
			return nil
		}
		mci.Status.TrafficBlockClusters = trafficBlockClusters
		updateErr := c.Client.Status().Update(ctx, mci)
		if updateErr == nil {
			return nil
		}

		updatedMCI := &networkingv1alpha1.MultiClusterIngress{}
		err := c.Client.Get(ctx, mciNamespacedName, updatedMCI)
		if err == nil {
			mci = updatedMCI.DeepCopy()
		} else {
			klog.Errorf("Failed to get updated multiClusterIngress(%s): %v", mciNamespacedName.String(), err)
		}
		return updateErr
	})
	if err != nil {
		klog.Errorf("Failed to sync multiClusterIngress(%s) trafficBlockClusters: %v", mciNamespacedName.String(), err)
		return err
	}
	klog.V(4).Infof("Success to sync multiClusterIngress(%s) trafficBlockClusters", mciNamespacedName.String())
	return nil
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
		if err = c.Get(ctx, client.ObjectKey{Namespace: mci.Namespace, Name: mci.Name}, updated); err == nil {
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
			RateLimiter: ratelimiterflag.DefaultControllerRateLimiter[controllerruntime.Request](c.RateLimiterOptions),
		})
	if err != nil {
		return err
	}

	if err = c.setupWatches(ctx, mciController, mgr); err != nil {
		return err
	}

	return nil
}

func (c *MCIController) setupWatches(ctx context.Context, mciController controller.Controller, mgr controllerruntime.Manager) error {
	mciEventChan := make(chan event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress])
	svcEventChan := make(chan event.TypedGenericEvent[*corev1.Service])

	mciEventHandler := newMultiClusterIngressEventHandler(ctx, c.Client, c.ProviderClassName)
	svcEventHandler := newServiceEventHandler(mciEventChan, c.Client)
	epsEventHandler := newEndpointSlicesEventHandler(svcEventChan)
	secEventHandler := newSecretEventHandler(mciEventChan, c.Client)
	clusterHandler := newClusterEventHandler(mciEventChan, c.Client)

	if err := mciController.Watch(source.Kind[*networkingv1alpha1.MultiClusterIngress](mgr.GetCache(), &networkingv1alpha1.MultiClusterIngress{}, mciEventHandler)); err != nil {
		return err
	}
	if err := mciController.Watch(source.Channel[*networkingv1alpha1.MultiClusterIngress](mciEventChan, mciEventHandler)); err != nil {
		return err
	}
	if err := mciController.Watch(source.Kind[*corev1.Service](mgr.GetCache(), &corev1.Service{}, svcEventHandler)); err != nil {
		return err
	}
	if err := mciController.Watch(source.Channel[*corev1.Service](svcEventChan, svcEventHandler)); err != nil {
		return err
	}
	if err := mciController.Watch(source.Kind[*discoveryv1.EndpointSlice](mgr.GetCache(), &discoveryv1.EndpointSlice{}, epsEventHandler)); err != nil {
		return err
	}
	if err := mciController.Watch(source.Kind[*corev1.Secret](mgr.GetCache(), &corev1.Secret{}, secEventHandler)); err != nil {
		return err
	}
	if err := mciController.Watch(source.Kind[*clusterv1alpha1.Cluster](mgr.GetCache(), &clusterv1alpha1.Cluster{}, clusterHandler)); err != nil {
		return err
	}
	return nil
}
