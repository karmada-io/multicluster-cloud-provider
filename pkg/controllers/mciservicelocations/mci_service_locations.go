package mciservicelocations

import (
	"context"
	"reflect"
	"sort"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
	"github.com/karmada-io/karmada/pkg/util/names"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/indexes"
)

// Controller is used to maintain information about the clusters in which
// the Service backend of the MultiClusterIngress resource resides.
type Controller struct {
	client.Client
	RateLimiterOptions ratelimiterflag.Options
}

// Reconcile performs a full reconciliation for the object referred to by the Request.
// The Controller will requeue the Request to be processed again if an error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (c *Controller) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).Infof("Reconciling MultiClusterIngress %s", req.NamespacedName.String())

	mci := &networkingv1alpha1.MultiClusterIngress{}
	if err := c.Client.Get(ctx, req.NamespacedName, mci); err != nil {
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}
		klog.InfoS("failed to get multiClusterIngress object", "NamespacedName", req.NamespacedName.String())
		return controllerruntime.Result{}, err
	}

	svcLocations, err := c.calculateServiceLocations(ctx, mci)
	if err != nil {
		return controllerruntime.Result{}, err
	}

	mci = mci.DeepCopy()
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if reflect.DeepEqual(svcLocations, mci.Status.ServiceLocations) {
			return nil
		}
		mci.Status.ServiceLocations = svcLocations
		updateErr := c.Client.Status().Update(ctx, mci)
		if updateErr == nil {
			return nil
		}

		updatedMCI := &networkingv1alpha1.MultiClusterIngress{}
		err = c.Client.Get(ctx, req.NamespacedName, updatedMCI)
		if err == nil {
			mci = updatedMCI.DeepCopy()
		} else {
			klog.Errorf("Failed to get updated multiClusterIngress(%s): %v", req.NamespacedName.String(), err)
		}
		return updateErr
	})
	if err != nil {
		klog.Errorf("Failed to sync multiClusterIngress(%s) service locations: %v", req.NamespacedName.String(), err)
		return controllerruntime.Result{}, err
	}
	klog.V(4).Infof("Success to sync multiClusterIngress(%s) service locations", req.NamespacedName.String())
	return controllerruntime.Result{}, nil
}

func (c *Controller) calculateServiceLocations(ctx context.Context, mci *networkingv1alpha1.MultiClusterIngress) ([]networkingv1alpha1.ServiceLocation, error) {
	backendSvcNames := indexes.BuildServiceRefIndexes(mci)
	sort.Strings(backendSvcNames)

	var svcLocations []networkingv1alpha1.ServiceLocation
	for _, svcName := range backendSvcNames {
		svcBinding := &workv1alpha2.ResourceBinding{}
		svcRBNamespacedName := types.NamespacedName{
			Namespace: mci.Namespace,
			Name:      names.GenerateBindingName("Service", svcName),
		}
		err := c.Client.Get(ctx, svcRBNamespacedName, svcBinding)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			klog.ErrorS(err, "failed to get service's related resourceBinding",
				"ResourceBinding", svcRBNamespacedName.String())
			return nil, err
		}

		svcLocations = append(svcLocations, networkingv1alpha1.ServiceLocation{
			Name:     svcName,
			Clusters: obtainBindingClusters(svcBinding),
		})
	}
	return svcLocations, nil
}

func obtainBindingClusters(rb *workv1alpha2.ResourceBinding) []string {
	clusters := sets.NewString()
	for _, cluster := range rb.Spec.Clusters {
		clusters.Insert(cluster.Name)
	}
	for _, requiredBy := range rb.Spec.RequiredBy {
		for _, cluster := range requiredBy.Clusters {
			clusters.Insert(cluster.Name)
		}
	}
	return clusters.List()
}

// SetupWithManager creates a controller and register to controller manager.
func (c *Controller) SetupWithManager(mgr controllerruntime.Manager) error {
	mciPredicateFuncs := predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool { return true },
		DeleteFunc: func(_ event.DeleteEvent) bool { return false },
		UpdateFunc: func(event event.UpdateEvent) bool {
			oldMCI := event.ObjectOld.(*networkingv1alpha1.MultiClusterIngress)
			newMCI := event.ObjectNew.(*networkingv1alpha1.MultiClusterIngress)
			var oldDefaultBackend, newDefaultBackend networkingv1.IngressBackend
			if oldMCI.Spec.DefaultBackend != nil {
				oldDefaultBackend = *oldMCI.Spec.DefaultBackend
			}
			if newMCI.Spec.DefaultBackend != nil {
				newDefaultBackend = *newMCI.Spec.DefaultBackend
			}
			return !reflect.DeepEqual(oldDefaultBackend, newDefaultBackend) ||
				!reflect.DeepEqual(oldMCI.Spec.Rules, newMCI.Spec.Rules)
		},
	}

	rbMapFunc := handler.MapFunc(
		func(_ context.Context, object client.Object) []reconcile.Request {
			var requests []reconcile.Request

			rb := object.(*workv1alpha2.ResourceBinding)
			if rb.Spec.Resource.APIVersion != "v1" || rb.Spec.Resource.Kind != "Service" {
				return nil
			}

			mciList := &networkingv1alpha1.MultiClusterIngressList{}
			if err := c.Client.List(context.Background(), mciList,
				client.InNamespace(rb.GetNamespace()),
				client.MatchingFields{indexes.IndexKeyServiceRefName: rb.Spec.Resource.Name}); err != nil {
				klog.Errorf("failed to fetch multiclusteringresses")
				return nil
			}

			for index := range mciList.Items {
				mci := &mciList.Items[index]
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: mci.Namespace, Name: mci.Name}})
			}
			return requests
		})

	rbPredicateFuncs := predicate.Funcs{
		CreateFunc: func(event event.CreateEvent) bool {
			rb := event.Object.(*workv1alpha2.ResourceBinding)
			return rb.Spec.Resource.APIVersion == "v1" && rb.Spec.Resource.Kind == "Service"
		},
		UpdateFunc: func(event event.UpdateEvent) bool {
			oldRB := event.ObjectOld.(*workv1alpha2.ResourceBinding)
			newRB := event.ObjectNew.(*workv1alpha2.ResourceBinding)
			if newRB.Spec.Resource.APIVersion != "v1" || newRB.Spec.Resource.Kind != "Service" {
				return false
			}
			return !reflect.DeepEqual(oldRB.Spec.Clusters, newRB.Spec.Clusters) ||
				!reflect.DeepEqual(oldRB.Spec.RequiredBy, newRB.Spec.RequiredBy)
		},
		DeleteFunc: func(event event.DeleteEvent) bool {
			rb := event.Object.(*workv1alpha2.ResourceBinding)
			return rb.Spec.Resource.APIVersion == "v1" && rb.Spec.Resource.Kind == "Service"
		},
	}

	return controllerruntime.NewControllerManagedBy(mgr).
		For(&networkingv1alpha1.MultiClusterIngress{}, builder.WithPredicates(mciPredicateFuncs)).
		Watches(&workv1alpha2.ResourceBinding{}, handler.EnqueueRequestsFromMapFunc(rbMapFunc), builder.WithPredicates(rbPredicateFuncs)).
		WithOptions(controller.Options{RateLimiter: ratelimiterflag.DefaultControllerRateLimiter[controllerruntime.Request](c.RateLimiterOptions)}).
		Complete(c)
}
