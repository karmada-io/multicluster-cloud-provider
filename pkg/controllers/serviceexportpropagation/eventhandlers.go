package serviceexportpropagation

import (
	"context"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	workv1alpha1 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/indexes"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/util"
)

func newServiceEventHandler(ctx context.Context, client client.Client) handler.TypedEventHandler[*corev1.Service, reconcile.Request] {
	return &serviceEventHandler{
		ctx:    ctx,
		client: client,
	}
}

var _ handler.TypedEventHandler[*corev1.Service, reconcile.Request] = (*serviceEventHandler)(nil)

type serviceEventHandler struct {
	ctx    context.Context
	client client.Client
}

func (h *serviceEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*corev1.Service], queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	mciList := &networkingv1alpha1.MultiClusterIngressList{}
	if err := h.client.List(h.ctx, mciList,
		client.InNamespace(e.Object.GetNamespace()),
		client.MatchingFields{indexes.IndexKeyServiceRefName: e.Object.GetName()}); err != nil {
		klog.Errorf("failed to fetch multiclusteringresses")
		return
	}

	if len(mciList.Items) > 0 {
		queue.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: e.Object.GetNamespace(),
				Name:      e.Object.GetName(),
			}})
		return
	}

	mcs := &networkingv1alpha1.MultiClusterService{}
	if err := h.client.Get(h.ctx, types.NamespacedName{
		Namespace: e.Object.GetNamespace(),
		Name:      e.Object.GetName(),
	}, mcs); err != nil {
		return
	}

	queue.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: e.Object.GetNamespace(),
			Name:      e.Object.GetName(),
		}})
}

func (h *serviceEventHandler) Update(_ context.Context, _ event.TypedUpdateEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// We only need to create ServiceExport based on the service and propagate it to
	// member clusters. Therefore, we do not need to pay attention to service update.
}

func (h *serviceEventHandler) Delete(_ context.Context, _ event.TypedDeleteEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// We will add an ownerReference to the service object on the ServiceExport
	// object, so that cleanup will be handled by gc controller.
}

func (h *serviceEventHandler) Generic(_ context.Context, e event.TypedGenericEvent[*corev1.Service], queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	queue.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: e.Object.GetNamespace(),
			Name:      e.Object.GetName(),
		}})
}

func newMultiClusterIngressEventHandler(ctx context.Context, client client.Client, svcEventChan chan<- event.TypedGenericEvent[*corev1.Service], providerClassName string) handler.TypedEventHandler[*networkingv1alpha1.MultiClusterIngress, reconcile.Request] {
	return &multiClusterIngressEventHandler{
		ctx:          ctx,
		client:       client,
		svcEventChan: svcEventChan,
		ingClassName: providerClassName,
	}
}

var _ handler.TypedEventHandler[*networkingv1alpha1.MultiClusterIngress, reconcile.Request] = (*multiClusterIngressEventHandler)(nil)

type multiClusterIngressEventHandler struct {
	ctx          context.Context
	client       client.Client
	svcEventChan chan<- event.TypedGenericEvent[*corev1.Service]
	ingClassName string
}

func (h *multiClusterIngressEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*networkingv1alpha1.MultiClusterIngress], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if !util.CheckIngressClassMatched(h.ctx, h.client, e.Object, h.ingClassName) {
		return
	}
	h.enqueueImpactedService(e.Object)
}

func (h *multiClusterIngressEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*networkingv1alpha1.MultiClusterIngress], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	mciOld := e.ObjectOld
	mciNew := e.ObjectNew
	if !util.CheckIngressClassMatched(h.ctx, h.client, mciNew, h.ingClassName) {
		return
	}

	// We only care about the service update:
	oldSvcRefs := indexes.BuildServiceRefIndexes(mciOld)
	newSvcRefs := indexes.BuildServiceRefIndexes(mciNew)
	if equality.Semantic.DeepEqual(oldSvcRefs, newSvcRefs) {
		return
	}

	var targetRefs []string
	for _, svc := range oldSvcRefs {
		if !slices.Contains(newSvcRefs, svc) {
			targetRefs = append(targetRefs, svc)
		}
	}
	for _, svc := range newSvcRefs {
		if !slices.Contains(oldSvcRefs, svc) {
			targetRefs = append(targetRefs, svc)
		}
	}

	for _, svc := range targetRefs {
		h.svcEventChan <- event.TypedGenericEvent[*corev1.Service]{
			Object: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: mciNew.Namespace,
					Name:      svc,
				}}}
	}
}

func (h *multiClusterIngressEventHandler) Delete(_ context.Context, e event.TypedDeleteEvent[*networkingv1alpha1.MultiClusterIngress], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if !util.CheckIngressClassMatched(h.ctx, h.client, e.Object, h.ingClassName) {
		return
	}
	h.enqueueImpactedService(e.Object)
}

func (h *multiClusterIngressEventHandler) Generic(_ context.Context, _ event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *multiClusterIngressEventHandler) enqueueImpactedService(mci *networkingv1alpha1.MultiClusterIngress) {
	svcRefs := indexes.BuildServiceRefIndexes(mci)
	for _, svc := range svcRefs {
		h.svcEventChan <- event.TypedGenericEvent[*corev1.Service]{
			Object: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: mci.Namespace,
					Name:      svc,
				}}}
	}
}

func newMultiClusterServiceEventHandler(
	ctx context.Context,
	client client.Client,
	svcEventChan chan<- event.TypedGenericEvent[*corev1.Service],
) handler.TypedEventHandler[*networkingv1alpha1.MultiClusterService, reconcile.Request] {
	return &multiClusterServiceEventHandler{
		ctx:          ctx,
		client:       client,
		svcEventChan: svcEventChan,
	}
}

var _ handler.TypedEventHandler[*networkingv1alpha1.MultiClusterService, reconcile.Request] = (*multiClusterServiceEventHandler)(nil)

type multiClusterServiceEventHandler struct {
	ctx          context.Context
	client       client.Client
	svcEventChan chan<- event.TypedGenericEvent[*corev1.Service]
}

func (h *multiClusterServiceEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*networkingv1alpha1.MultiClusterService], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedService(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *multiClusterServiceEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*networkingv1alpha1.MultiClusterService], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	mcsOld := e.ObjectOld
	mcsNew := e.ObjectNew

	// Only care about the update events below:
	if equality.Semantic.DeepEqual(mcsOld.Annotations, mcsNew.Annotations) &&
		equality.Semantic.DeepEqual(mcsOld.Spec.Types, mcsNew.Spec.Types) &&
		equality.Semantic.DeepEqual(mcsOld.Spec.Ports, mcsNew.Spec.Ports) {
		return
	}

	h.enqueueImpactedService(mcsNew.Namespace, mcsNew.Name)
}

func (h *multiClusterServiceEventHandler) Delete(_ context.Context, e event.TypedDeleteEvent[*networkingv1alpha1.MultiClusterService], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedService(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *multiClusterServiceEventHandler) Generic(_ context.Context, _ event.TypedGenericEvent[*networkingv1alpha1.MultiClusterService], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *multiClusterServiceEventHandler) enqueueImpactedService(namespace, name string) {
	h.svcEventChan <- event.TypedGenericEvent[*corev1.Service]{
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			}}}
}

func newResourceBindingEventHandler(svcEventChan chan<- event.TypedGenericEvent[*corev1.Service]) handler.TypedEventHandler[*workv1alpha1.ResourceBinding, reconcile.Request] {
	return &resourceBindingEventHandler{
		svcEventChan: svcEventChan,
	}
}

var _ handler.TypedEventHandler[*workv1alpha1.ResourceBinding, reconcile.Request] = (*resourceBindingEventHandler)(nil)

type resourceBindingEventHandler struct {
	svcEventChan chan<- event.TypedGenericEvent[*corev1.Service]
}

func (h *resourceBindingEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*workv1alpha1.ResourceBinding], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// The distribution feature involves directly creating rb objects,
	// so it is necessary to care about the rb creation event.
	if e.Object.Spec.Resource.Kind != "Service" {
		return
	}
	h.svcEventChan <- event.TypedGenericEvent[*corev1.Service]{
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: e.Object.Spec.Resource.Namespace,
				Name:      e.Object.Spec.Resource.Name,
			}}}
}

func (h *resourceBindingEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*workv1alpha1.ResourceBinding], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	rbOlb := e.ObjectOld
	rbNew := e.ObjectNew

	resource := rbNew.Spec.Resource
	if resource.Kind != "Service" {
		return
	}

	scheduleResultOld := getClusterResults(rbOlb.Spec)
	scheduleResultNew := getClusterResults(rbNew.Spec)
	if equality.Semantic.DeepEqual(scheduleResultOld, scheduleResultNew) {
		return
	}

	h.svcEventChan <- event.TypedGenericEvent[*corev1.Service]{
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: resource.Namespace,
				Name:      resource.Name,
			}}}
}

func (h *resourceBindingEventHandler) Delete(_ context.Context, _ event.TypedDeleteEvent[*workv1alpha1.ResourceBinding], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// The deletion event of the resourceBinding will be
	// processed by the deletion event of service.
}

func (h *resourceBindingEventHandler) Generic(_ context.Context, _ event.TypedGenericEvent[*workv1alpha1.ResourceBinding], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}
