package multiclusterservice

import (
	"context"
	"strings"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/karmada-io/multicluster-cloud-provider/pkg/util"
)

func newMultiClusterServiceEventHandler() handler.EventHandler {
	return &multiClusterServiceEventHandler{}
}

var _ handler.EventHandler = (*multiClusterServiceEventHandler)(nil)

type multiClusterServiceEventHandler struct {
}

func (h *multiClusterServiceEventHandler) Create(_ context.Context, e event.CreateEvent, queue workqueue.RateLimitingInterface) {
	mcs := e.Object.(*networkingv1alpha1.MultiClusterService)
	if !util.MCSContainLoadBalanceType(mcs) {
		return
	}

	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: mcs.Namespace,
		Name:      mcs.Name,
	}})
}

func (h *multiClusterServiceEventHandler) Update(_ context.Context, e event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	mcsOld := e.ObjectOld.(*networkingv1alpha1.MultiClusterService)
	mcsNew := e.ObjectNew.(*networkingv1alpha1.MultiClusterService)
	if !util.MCSContainLoadBalanceType(mcsOld) && !util.MCSContainLoadBalanceType(mcsNew) {
		return
	}

	// We only care about the update events below:
	if equality.Semantic.DeepEqual(mcsOld.Annotations, mcsNew.Annotations) &&
		equality.Semantic.DeepEqual(mcsOld.Spec, mcsNew.Spec) &&
		equality.Semantic.DeepEqual(mcsOld.DeletionTimestamp.IsZero(), mcsNew.DeletionTimestamp.IsZero()) {
		return
	}

	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: mcsNew.GetNamespace(),
		Name:      mcsNew.GetName(),
	}})
}

func (h *multiClusterServiceEventHandler) Delete(_ context.Context, _ event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	// Since finalizer is added to the MultiClusterService object,
	// the delete event is processed by the update event.
}

func (h *multiClusterServiceEventHandler) Generic(_ context.Context, e event.GenericEvent, queue workqueue.RateLimitingInterface) {
	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: e.Object.GetNamespace(),
		Name:      e.Object.GetName(),
	}})
}

func newServiceEventHandler(mcsEventChan chan<- event.GenericEvent, client client.Client) handler.EventHandler {
	return &serviceEventHandler{
		mcsEventChan: mcsEventChan,
		client:       client,
	}
}

var _ handler.EventHandler = (*serviceEventHandler)(nil)

type serviceEventHandler struct {
	mcsEventChan chan<- event.GenericEvent
	client       client.Client
}

func (h *serviceEventHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedMCS(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) Update(_ context.Context, e event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	svcOld := e.ObjectOld.(*corev1.Service)
	svcNew := e.ObjectNew.(*corev1.Service)

	// We only care about the update events below:
	if equality.Semantic.DeepEqual(svcOld.Annotations, svcNew.Annotations) &&
		equality.Semantic.DeepEqual(svcOld.Spec, svcNew.Spec) {
		return
	}

	h.enqueueImpactedMCS(svcNew.Namespace, svcNew.Name)
}

func (h *serviceEventHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedMCS(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) Generic(_ context.Context, e event.GenericEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedMCS(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) enqueueImpactedMCS(svcNamespace, svcName string) {
	h.mcsEventChan <- event.GenericEvent{
		Object: &networkingv1alpha1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: svcNamespace,
				Name:      svcName,
			}}}
}

// endpointSlicePrefix is the prefix of service derived from ServiceImport.
const derivedServicePrefix = "derived-"

func newEndpointSlicesEventHandler(svcEventChan chan<- event.GenericEvent) handler.EventHandler {
	return &endpointSlicesEventHandler{
		svcEventChan: svcEventChan,
	}
}

var _ handler.EventHandler = (*endpointSlicesEventHandler)(nil)

type endpointSlicesEventHandler struct {
	svcEventChan chan<- event.GenericEvent
}

func (h *endpointSlicesEventHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedSvc(e.Object)
}

func (h *endpointSlicesEventHandler) Update(_ context.Context, e event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedSvc(e.ObjectNew)
}

func (h *endpointSlicesEventHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedSvc(e.Object)
}

func (h *endpointSlicesEventHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
}

func (h *endpointSlicesEventHandler) enqueueImpactedSvc(obj client.Object) {
	svcName, ok := obj.GetLabels()[discoveryv1.LabelServiceName]
	if !ok {
		klog.Warning("Can not get the key(%s) with the endpointSlices object(%s/%s)",
			discoveryv1.LabelServiceName, obj.GetNamespace(), obj.GetName())
		return
	}

	h.svcEventChan <- event.GenericEvent{
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: obj.GetNamespace(),
				Name:      strings.TrimPrefix(svcName, derivedServicePrefix),
			}}}
}
