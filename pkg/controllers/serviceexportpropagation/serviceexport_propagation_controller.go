package serviceexportpropagation

import (
	"context"
	"fmt"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	policyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1"
	workv1alpha1 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/source"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/indexes"
)

// ControllerName it the controller name that will be used when reporting events.
const ControllerName = "serviceexport-propagation-controller"

const propagationPolicyPrefix = "propagate-service-"

// Controller will propagate ServiceExport resource into member clusters.
type Controller struct {
	client.Client
	EventRecorder      record.EventRecorder
	RateLimiterOptions ratelimiterflag.Options
	ProviderClassName  string
}

// Reconcile performs a full reconciliation for the Service object and
// propagates ServiceExport resource into member clusters.
func (c *Controller) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).InfoS("Reconciling Service", "namespace", req.Namespace, "name", req.Name)

	svc := &corev1.Service{}
	if err := c.Client.Get(ctx, req.NamespacedName, svc); err != nil {
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}
		return controllerruntime.Result{}, err
	}

	if !svc.DeletionTimestamp.IsZero() {
		return controllerruntime.Result{}, nil
	}

	mciList := &networkingv1alpha1.MultiClusterIngressList{}
	if err := c.Client.List(context.Background(), mciList,
		client.InNamespace(svc.Namespace),
		client.MatchingFields{indexes.IndexKeyServiceRefName: svc.Name}); err != nil {
		klog.Errorf("failed to fetch multiclusteringresses")
		return controllerruntime.Result{}, err
	}

	if len(mciList.Items) > 0 {
		return c.ensureServiceExport(ctx, svc)
	}
	return c.removeServiceExport(ctx, svc)
}

func (c *Controller) ensureServiceExport(ctx context.Context, svc *corev1.Service) (controllerruntime.Result, error) {
	// 1. make sure serviceExport exist
	svcExport := &mcsv1alpha1.ServiceExport{}
	err := c.Client.Get(ctx, types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}, svcExport)
	if err != nil && !apierrors.IsNotFound(err) {
		klog.ErrorS(err, "failed to get serviceExport", "namespace", svc.Namespace, "name", svc.Name)
		return controllerruntime.Result{}, err
	}

	// 2. if serviceExport not exist, just create it
	if apierrors.IsNotFound(err) {
		svcExport = createServiceExportTemplate(svc)
		err = c.Client.Create(ctx, svcExport)
		if err != nil {
			klog.ErrorS(err, "failed to create serviceExport", "namespace", svc.Namespace, "name", svc.Name)
			return controllerruntime.Result{}, err
		}
		klog.V(4).InfoS("success to create serviceExport", "namespace", svc.Namespace, "name", svc.Name)
	}

	// 3. get service's scheduled result
	clusters, err := c.getBindingClusterResults(ctx, svc)
	if err != nil {
		return controllerruntime.Result{}, err
	}
	if len(clusters) == 0 {
		klog.V(4).InfoS("service's scheduled result length is zero", "namespace", svc.Namespace, "name", svc.Name)
		return controllerruntime.Result{}, nil
	}
	klog.V(4).InfoS("service's cluster results", "namespace", svc.Namespace, "name", svc.Name, "clusters", clusters)

	// 4. make sure propagationPolicy exist
	policyName := fmt.Sprintf("%s%s", propagationPolicyPrefix, svc.Name)
	policy := &policyv1alpha1.PropagationPolicy{}
	err = c.Client.Get(ctx, types.NamespacedName{Namespace: svc.Namespace, Name: policyName}, policy)
	if err != nil && !apierrors.IsNotFound(err) {
		klog.ErrorS(err, "failed to get policy", "namespace", svc.Namespace, "name", policyName)
		return controllerruntime.Result{}, err
	}

	// 5. if propagationPolicy not exist, just create it
	if apierrors.IsNotFound(err) {
		policy = createPropagationPolicyTemplate(policyName, svc, clusters)
		err = c.Client.Create(ctx, policy)
		if err != nil {
			klog.ErrorS(err, "failed to create policy", "namespace", svc.Namespace, "name", policyName)
			return controllerruntime.Result{}, err
		}
		klog.V(4).InfoS("success to create policy", "namespace", svc.Namespace, "name", policyName)
		return controllerruntime.Result{}, nil
	}

	// 6. propagationPolicy already exist, just update it
	if equality.Semantic.DeepEqual(policy.Spec.Placement.ClusterAffinity.ClusterNames, clusters) {
		klog.V(4).InfoS("there is no need to update policy", "namespace", svc.Namespace, "name", policyName)
		return controllerruntime.Result{}, nil
	}
	policyCopy := policy.DeepCopy()
	policyCopy.Spec.Placement.ClusterAffinity.ClusterNames = clusters
	err = c.Client.Update(ctx, policyCopy)
	if err != nil {
		klog.ErrorS(err, "failed to update policy", "namespace", svc.Namespace, "name", policyName)
		return controllerruntime.Result{}, err
	}
	klog.V(4).InfoS("success to update policy", "namespace", svc.Namespace, "name", policyName)
	return controllerruntime.Result{}, nil
}

func createServiceExportTemplate(svc *corev1.Service) *mcsv1alpha1.ServiceExport {
	return &mcsv1alpha1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svc.Namespace,
			Name:      svc.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(svc, corev1.SchemeGroupVersion.WithKind("Service")),
			},
		},
	}
}

func createPropagationPolicyTemplate(policyName string, svc *corev1.Service, clusters []string) *policyv1alpha1.PropagationPolicy {
	return &policyv1alpha1.PropagationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svc.Namespace,
			Name:      policyName,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(svc, corev1.SchemeGroupVersion.WithKind("Service")),
			},
		},
		Spec: policyv1alpha1.PropagationSpec{
			ResourceSelectors: []policyv1alpha1.ResourceSelector{{
				APIVersion: mcsv1alpha1.GroupVersion.String(),
				Kind:       "ServiceExport",
				Name:       svc.Name,
			}},
			Placement: policyv1alpha1.Placement{
				ClusterAffinity: &policyv1alpha1.ClusterAffinity{
					ClusterNames: clusters,
				}}},
	}
}

func (c *Controller) getBindingClusterResults(ctx context.Context, svc *corev1.Service) ([]string, error) {
	bindings := &workv1alpha1.ResourceBindingList{}
	err := c.Client.List(ctx, bindings, client.InNamespace(svc.Namespace))
	if err != nil {
		klog.ErrorS(err, "failed to list resourceBinding", "namespace", svc.Namespace)
		return nil, err
	}

	var targetBindingSpec workv1alpha1.ResourceBindingSpec
	for _, binding := range bindings.Items {
		resource := binding.Spec.Resource
		if resource.Kind == "Service" && resource.Name == svc.Name {
			targetBindingSpec = binding.Spec
			break
		}
	}

	return getClusterResults(targetBindingSpec), nil
}

func getClusterResults(rbSpec workv1alpha1.ResourceBindingSpec) []string {
	clusterResults := sets.NewString()
	for _, cluster := range rbSpec.Clusters {
		clusterResults.Insert(cluster.Name)
	}
	for _, requiredBy := range rbSpec.RequiredBy {
		for _, cluster := range requiredBy.Clusters {
			clusterResults.Insert(cluster.Name)
		}
	}
	return clusterResults.List()
}

func (c *Controller) removeServiceExport(ctx context.Context, svc *corev1.Service) (controllerruntime.Result, error) {
	err := c.deleteServiceExport(ctx, svc)
	if err != nil {
		return controllerruntime.Result{}, err
	}

	err = c.deletePropagationPolicy(ctx, svc)
	if err != nil {
		return controllerruntime.Result{}, err
	}

	return controllerruntime.Result{}, nil
}

func (c *Controller) deleteServiceExport(ctx context.Context, svc *corev1.Service) error {
	svcExport := &mcsv1alpha1.ServiceExport{}
	err := c.Client.Get(ctx, types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}, svcExport)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		klog.ErrorS(err, "failed to get serviceExport", "namespace", svc.Namespace, "name", svc.Name)
		return err
	}

	err = c.Client.Delete(ctx, svcExport)
	if err != nil && !apierrors.IsNotFound(err) {
		klog.ErrorS(err, "failed to delete serviceExport", "namespace", svc.Namespace, "name", svc.Name)
		return err
	}
	klog.V(4).InfoS("success to delete serviceExport", "namespace", svc.Namespace, "name", svc.Name)
	return nil
}

func (c *Controller) deletePropagationPolicy(ctx context.Context, svc *corev1.Service) error {
	policyName := fmt.Sprintf("%s%s", propagationPolicyPrefix, svc.Name)
	policy := &policyv1alpha1.PropagationPolicy{}
	err := c.Client.Get(ctx, types.NamespacedName{Namespace: svc.Namespace, Name: policyName}, policy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		klog.ErrorS(err, "failed to get policy", "namespace", svc.Namespace, "name", policyName)
		return err
	}

	err = c.Client.Delete(ctx, policy)
	if err != nil && !apierrors.IsNotFound(err) {
		klog.ErrorS(err, "failed to delete policy", "namespace", svc.Namespace, "name", policyName)
		return err
	}
	klog.V(4).InfoS("success to delete policy", "namespace", svc.Namespace, "name", policyName)
	return nil
}

// SetupWithManager creates a controller and register to controller manager.
func (c *Controller) SetupWithManager(ctx context.Context, mgr controllerruntime.Manager) error {
	serviceExportController, err := controller.New(ControllerName, mgr,
		controller.Options{
			Reconciler:  c,
			RateLimiter: ratelimiterflag.DefaultControllerRateLimiter(c.RateLimiterOptions),
		})
	if err != nil {
		return err
	}

	if err = c.setupWatches(ctx, serviceExportController); err != nil {
		return err
	}

	return nil
}

func (c *Controller) setupWatches(ctx context.Context, serviceExportController controller.Controller) error {
	svcEventChan := make(chan event.GenericEvent)

	svcEventHandler := newServiceEventHandler(c.Client)
	mciEventHandler := newMultiClusterIngressEventHandler(ctx, c.Client, svcEventChan, c.ProviderClassName)
	rbEventHandler := newResourceBindingEventHandler(svcEventChan)

	if err := serviceExportController.Watch(&source.Kind{Type: &corev1.Service{}}, svcEventHandler); err != nil {
		return err
	}
	if err := serviceExportController.Watch(&source.Channel{Source: svcEventChan}, svcEventHandler); err != nil {
		return err
	}
	if err := serviceExportController.Watch(&source.Kind{Type: &networkingv1alpha1.MultiClusterIngress{}}, mciEventHandler); err != nil {
		return err
	}
	if err := serviceExportController.Watch(&source.Kind{Type: &workv1alpha1.ResourceBinding{}}, rbEventHandler); err != nil {
		return err
	}
	return nil
}
