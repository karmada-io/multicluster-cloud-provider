package multiclusteringress

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

	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/indexes"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/util"
)

func newMultiClusterIngressEventHandler(ctx context.Context, client client.Client, providerClassName string) handler.EventHandler {
	return &multiClusterIngressEventHandler{
		ctx:          ctx,
		client:       client,
		ingClassName: providerClassName,
	}
}

var _ handler.EventHandler = (*multiClusterIngressEventHandler)(nil)

type multiClusterIngressEventHandler struct {
	ctx          context.Context
	client       client.Client
	ingClassName string
}

func (h *multiClusterIngressEventHandler) Create(_ context.Context, e event.CreateEvent, queue workqueue.RateLimitingInterface) {
	mci := e.Object.(*networkingv1alpha1.MultiClusterIngress)
	if !util.CheckIngressClassMatched(h.ctx, h.client, mci, h.ingClassName) {
		return
	}
	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: e.Object.GetNamespace(),
		Name:      e.Object.GetName(),
	}})
}

func (h *multiClusterIngressEventHandler) Update(_ context.Context, e event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	mciOld := e.ObjectOld.(*networkingv1alpha1.MultiClusterIngress)
	mciNew := e.ObjectNew.(*networkingv1alpha1.MultiClusterIngress)
	if !util.CheckIngressClassMatched(h.ctx, h.client, mciNew, h.ingClassName) {
		return
	}

	// We only care about the update events below:
	if equality.Semantic.DeepEqual(mciOld.Annotations, mciNew.Annotations) &&
		equality.Semantic.DeepEqual(mciOld.Spec, mciNew.Spec) &&
		equality.Semantic.DeepEqual(mciOld.DeletionTimestamp.IsZero(), mciNew.DeletionTimestamp.IsZero()) {
		return
	}

	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: mciNew.Namespace,
		Name:      mciNew.Name,
	}})
}

func (h *multiClusterIngressEventHandler) Delete(_ context.Context, _ event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	// Since finalizer is added to the multiClusterIngress object,
	// the delete event is processed by the update event.
}

func (h *multiClusterIngressEventHandler) Generic(_ context.Context, e event.GenericEvent, queue workqueue.RateLimitingInterface) {
	mci := e.Object.(*networkingv1alpha1.MultiClusterIngress)
	if !util.CheckIngressClassMatched(h.ctx, h.client, mci, h.ingClassName) {
		return
	}
	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: e.Object.GetNamespace(),
		Name:      e.Object.GetName(),
	}})
}

func newServiceEventHandler(mciEventChan chan<- event.GenericEvent, client client.Client) handler.EventHandler {
	return &serviceEventHandler{
		mciEventChan: mciEventChan,
		client:       client,
	}
}

var _ handler.EventHandler = (*serviceEventHandler)(nil)

type serviceEventHandler struct {
	mciEventChan chan<- event.GenericEvent
	client       client.Client
}

func (h *serviceEventHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedMCI(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) Update(_ context.Context, e event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	svcOld := e.ObjectOld.(*corev1.Service)
	svcNew := e.ObjectNew.(*corev1.Service)

	// We only care about the update events below:
	if equality.Semantic.DeepEqual(svcOld.Annotations, svcNew.Annotations) &&
		equality.Semantic.DeepEqual(svcOld.Spec, svcNew.Spec) &&
		equality.Semantic.DeepEqual(svcOld.DeletionTimestamp.IsZero(), svcNew.DeletionTimestamp.IsZero()) {
		return
	}

	h.enqueueImpactedMCI(svcNew.Namespace, svcNew.Name)
}

func (h *serviceEventHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedMCI(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) Generic(_ context.Context, e event.GenericEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedMCI(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) enqueueImpactedMCI(svcNamespace, svcName string) {
	mciList := &networkingv1alpha1.MultiClusterIngressList{}
	if err := h.client.List(context.Background(), mciList,
		client.InNamespace(svcNamespace),
		client.MatchingFields{indexes.IndexKeyServiceRefName: svcName}); err != nil {
		klog.Errorf("failed to fetch multiclusteringresses")
		return
	}

	for index := range mciList.Items {
		mci := &mciList.Items[index]
		h.mciEventChan <- event.GenericEvent{
			Object: mci,
		}
	}
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
		klog.Warningf("Can not get the key(%s) with the endpointSlices object(%s/%s)",
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

func newSecretEventHandler(mciEventChan chan<- event.GenericEvent, client client.Client) handler.EventHandler {
	return &secretEventHandler{
		mciEventChan: mciEventChan,
		client:       client,
	}
}

var _ handler.EventHandler = (*secretEventHandler)(nil)

type secretEventHandler struct {
	mciEventChan chan<- event.GenericEvent
	client       client.Client
}

func (h *secretEventHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedMCI(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *secretEventHandler) Update(_ context.Context, e event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	secretOld := e.ObjectOld.(*corev1.Secret)
	secretNew := e.ObjectNew.(*corev1.Secret)

	if equality.Semantic.DeepEqual(secretOld.Annotations, secretNew.Annotations) &&
		equality.Semantic.DeepEqual(secretOld.Data, secretNew.Data) &&
		equality.Semantic.DeepEqual(secretOld.StringData, secretNew.StringData) {
		return
	}

	h.enqueueImpactedMCI(secretNew.Namespace, secretNew.Name)
}

func (h *secretEventHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	h.enqueueImpactedMCI(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *secretEventHandler) enqueueImpactedMCI(secretNamespace, secretName string) {
	mciList := &networkingv1alpha1.MultiClusterIngressList{}
	if err := h.client.List(context.Background(), mciList,
		client.InNamespace(secretNamespace),
		client.MatchingFields{indexes.IndexKeySecretRefName: secretName}); err != nil {
		klog.Errorf("failed to fetch multiclusteringresses")
		return
	}

	for index := range mciList.Items {
		mci := &mciList.Items[index]
		h.mciEventChan <- event.GenericEvent{
			Object: mci,
		}
	}
}

func (h *secretEventHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
}
