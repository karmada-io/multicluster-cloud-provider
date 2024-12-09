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

func newMultiClusterServiceEventHandler() handler.TypedEventHandler[*networkingv1alpha1.MultiClusterService, reconcile.Request] {
	return &multiClusterServiceEventHandler{}
}

var _ handler.TypedEventHandler[*networkingv1alpha1.MultiClusterService, reconcile.Request] = (*multiClusterServiceEventHandler)(nil)

type multiClusterServiceEventHandler struct {
}

func (h *multiClusterServiceEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*networkingv1alpha1.MultiClusterService], queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if !util.MCSContainLoadBalanceType(e.Object) {
		return
	}

	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: e.Object.Namespace,
		Name:      e.Object.Name,
	}})
}

func (h *multiClusterServiceEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*networkingv1alpha1.MultiClusterService], queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	mcsOld := e.ObjectOld
	mcsNew := e.ObjectNew
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

func (h *multiClusterServiceEventHandler) Delete(_ context.Context, _ event.TypedDeleteEvent[*networkingv1alpha1.MultiClusterService], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// Since finalizer is added to the MultiClusterService object,
	// the delete event is processed by the update event.
}

func (h *multiClusterServiceEventHandler) Generic(_ context.Context, e event.TypedGenericEvent[*networkingv1alpha1.MultiClusterService], queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: e.Object.GetNamespace(),
		Name:      e.Object.GetName(),
	}})
}

func newServiceEventHandler(mcsEventChan chan<- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterService], client client.Client) handler.TypedEventHandler[*corev1.Service, reconcile.Request] {
	return &serviceEventHandler{
		mcsEventChan: mcsEventChan,
		client:       client,
	}
}

var _ handler.TypedEventHandler[*corev1.Service, reconcile.Request] = (*serviceEventHandler)(nil)

type serviceEventHandler struct {
	mcsEventChan chan<- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterService]
	client       client.Client
}

func (h *serviceEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedMCS(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	svcOld := e.ObjectOld
	svcNew := e.ObjectNew

	// We only care about the update events below:
	if equality.Semantic.DeepEqual(svcOld.Annotations, svcNew.Annotations) &&
		equality.Semantic.DeepEqual(svcOld.Spec, svcNew.Spec) {
		return
	}

	h.enqueueImpactedMCS(svcNew.Namespace, svcNew.Name)
}

func (h *serviceEventHandler) Delete(_ context.Context, e event.TypedDeleteEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedMCS(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) Generic(_ context.Context, e event.TypedGenericEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedMCS(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) enqueueImpactedMCS(svcNamespace, svcName string) {
	h.mcsEventChan <- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterService]{
		Object: &networkingv1alpha1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: svcNamespace,
				Name:      svcName,
			}}}
}

// endpointSlicePrefix is the prefix of service derived from ServiceImport.
const derivedServicePrefix = "derived-"

func newEndpointSlicesEventHandler(svcEventChan chan<- event.TypedGenericEvent[*corev1.Service]) handler.TypedEventHandler[*discoveryv1.EndpointSlice, reconcile.Request] {
	return &endpointSlicesEventHandler{
		svcEventChan: svcEventChan,
	}
}

var _ handler.TypedEventHandler[*discoveryv1.EndpointSlice, reconcile.Request] = (*endpointSlicesEventHandler)(nil)

type endpointSlicesEventHandler struct {
	svcEventChan chan<- event.TypedGenericEvent[*corev1.Service]
}

func (h *endpointSlicesEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*discoveryv1.EndpointSlice], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedSvc(e.Object)
}

func (h *endpointSlicesEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*discoveryv1.EndpointSlice], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedSvc(e.ObjectNew)
}

func (h *endpointSlicesEventHandler) Delete(_ context.Context, e event.TypedDeleteEvent[*discoveryv1.EndpointSlice], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedSvc(e.Object)
}

func (h *endpointSlicesEventHandler) Generic(_ context.Context, _ event.TypedGenericEvent[*discoveryv1.EndpointSlice], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *endpointSlicesEventHandler) enqueueImpactedSvc(obj client.Object) {
	svcName, ok := obj.GetLabels()[discoveryv1.LabelServiceName]
	if !ok {
		klog.Warningf("Can not get the key(%s) with the endpointSlices object(%s/%s)",
			discoveryv1.LabelServiceName, obj.GetNamespace(), obj.GetName())
		return
	}

	h.svcEventChan <- event.TypedGenericEvent[*corev1.Service]{
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: obj.GetNamespace(),
				Name:      strings.TrimPrefix(svcName, derivedServicePrefix),
			}}}
}
