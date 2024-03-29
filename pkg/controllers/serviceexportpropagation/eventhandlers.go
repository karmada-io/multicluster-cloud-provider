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

func newServiceEventHandler(ctx context.Context, client client.Client) handler.EventHandler {
	return &serviceEventHandler{
		ctx:    ctx,
		client: client,
	}
}

var _ handler.EventHandler = (*serviceEventHandler)(nil)

type serviceEventHandler struct {
	ctx    context.Context
	client client.Client
}

func (h *serviceEventHandler) Create(_ context.Context, e event.CreateEvent, queue workqueue.RateLimitingInterface) {
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

func (h *serviceEventHandler) Update(_ context.Context, _ event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	// We only need to create ServiceExport based on the service and propagate it to
	// member clusters. Therefore, we do not need to pay attention to service update.
}

func (h *serviceEventHandler) Delete(_ context.Context, _ event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	// We will add an ownerReference to the service object on the ServiceExport
	// object, so that cleanup will be handled by gc controller.
}

func (h *serviceEventHandler) Generic(_ context.Context, e event.GenericEvent, queue workqueue.RateLimitingInterface) {
	queue.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: e.Object.GetNamespace(),
			Name:      e.Object.GetName(),
		}})
}

func newMultiClusterIngressEventHandler(ctx context.Context, client client.Client, svcEventChan chan<- event.GenericEvent, providerClassName string) handler.EventHandler {
	return &multiClusterIngressEventHandler{
		ctx:          ctx,
		client:       client,
		svcEventChan: svcEventChan,
		ingClassName: providerClassName,
	}
}

var _ handler.EventHandler = (*multiClusterIngressEventHandler)(nil)

type multiClusterIngressEventHandler struct {
	ctx          context.Context
	client       client.Client
	svcEventChan chan<- event.GenericEvent
	ingClassName string
}

func (h *multiClusterIngressEventHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	mci := e.Object.(*networkingv1alpha1.MultiClusterIngress)
	if !util.CheckIngressClassMatched(h.ctx, h.client, mci, h.ingClassName) {
		return
	}
	h.enqueueImpactedService(mci)
}

func (h *multiClusterIngressEventHandler) Update(_ context.Context, e event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	mciOld := e.ObjectOld.(*networkingv1alpha1.MultiClusterIngress)
	mciNew := e.ObjectNew.(*networkingv1alpha1.MultiClusterIngress)
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
		h.svcEventChan <- event.GenericEvent{
			Object: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: mciNew.Namespace,
					Name:      svc,
				}}}
	}
}

func (h *multiClusterIngressEventHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	mci := e.Object.(*networkingv1alpha1.MultiClusterIngress)
	if !util.CheckIngressClassMatched(h.ctx, h.client, mci, h.ingClassName) {
		return
	}
	h.enqueueImpactedService(mci)
}

func (h *multiClusterIngressEventHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
}

func (h *multiClusterIngressEventHandler) enqueueImpactedService(mci *networkingv1alpha1.MultiClusterIngress) {
	svcRefs := indexes.BuildServiceRefIndexes(mci)
	for _, svc := range svcRefs {
		h.svcEventChan <- event.GenericEvent{
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
	svcEventChan chan<- event.GenericEvent,
) handler.EventHandler {
	return &multiClusterServiceEventHandler{
		ctx:          ctx,
		client:       client,
		svcEventChan: svcEventChan,
	}
}

var _ handler.EventHandler = (*multiClusterServiceEventHandler)(nil)

type multiClusterServiceEventHandler struct {
	ctx          context.Context
	client       client.Client
	svcEventChan chan<- event.GenericEvent
}

func (h *multiClusterServiceEventHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedService(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *multiClusterServiceEventHandler) Update(_ context.Context, e event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	mcsOld := e.ObjectOld.(*networkingv1alpha1.MultiClusterService)
	mcsNew := e.ObjectNew.(*networkingv1alpha1.MultiClusterService)

	// Only care about the update events below:
	if equality.Semantic.DeepEqual(mcsOld.Annotations, mcsNew.Annotations) &&
		equality.Semantic.DeepEqual(mcsOld.Spec.Types, mcsNew.Spec.Types) &&
		equality.Semantic.DeepEqual(mcsOld.Spec.Ports, mcsNew.Spec.Ports) {
		return
	}

	h.enqueueImpactedService(mcsNew.Namespace, mcsNew.Name)
}

func (h *multiClusterServiceEventHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedService(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *multiClusterServiceEventHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
}

func (h *multiClusterServiceEventHandler) enqueueImpactedService(namespace, name string) {
	h.svcEventChan <- event.GenericEvent{
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			}}}
}

func newResourceBindingEventHandler(svcEventChan chan<- event.GenericEvent) handler.EventHandler {
	return &resourceBindingEventHandler{
		svcEventChan: svcEventChan,
	}
}

var _ handler.EventHandler = (*resourceBindingEventHandler)(nil)

type resourceBindingEventHandler struct {
	svcEventChan chan<- event.GenericEvent
}

func (h *resourceBindingEventHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	// The distribution feature involves directly creating rb objects,
	// so it is necessary to care about the rb creation event.
	rb := e.Object.(*workv1alpha1.ResourceBinding)
	if rb.Spec.Resource.Kind != "Service" {
		return
	}
	h.svcEventChan <- event.GenericEvent{
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: rb.Spec.Resource.Namespace,
				Name:      rb.Spec.Resource.Name,
			}}}
}

func (h *resourceBindingEventHandler) Update(_ context.Context, e event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	rbOlb := e.ObjectOld.(*workv1alpha1.ResourceBinding)
	rbNew := e.ObjectNew.(*workv1alpha1.ResourceBinding)

	resource := rbNew.Spec.Resource
	if resource.Kind != "Service" {
		return
	}

	scheduleResultOld := getClusterResults(rbOlb.Spec)
	scheduleResultNew := getClusterResults(rbNew.Spec)
	if equality.Semantic.DeepEqual(scheduleResultOld, scheduleResultNew) {
		return
	}

	h.svcEventChan <- event.GenericEvent{
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: resource.Namespace,
				Name:      resource.Name,
			}}}
}

func (h *resourceBindingEventHandler) Delete(_ context.Context, _ event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	// The deletion event of the resourceBinding will be
	// processed by the deletion event of service.
}

func (h *resourceBindingEventHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
}
