package multiclusteringress

import (
	"context"
	"strings"

	clusterv1alpha1 "github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	remedyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/remedy/v1alpha1"
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

func newMultiClusterIngressEventHandler(ctx context.Context, client client.Client, providerClassName string) handler.TypedEventHandler[*networkingv1alpha1.MultiClusterIngress, reconcile.Request] {
	return &multiClusterIngressEventHandler{
		ctx:          ctx,
		client:       client,
		ingClassName: providerClassName,
	}
}

var _ handler.TypedEventHandler[*networkingv1alpha1.MultiClusterIngress, reconcile.Request] = (*multiClusterIngressEventHandler)(nil)

type multiClusterIngressEventHandler struct {
	ctx          context.Context
	client       client.Client
	ingClassName string
}

func (h *multiClusterIngressEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*networkingv1alpha1.MultiClusterIngress], queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	klog.Infof("mci(%s/%s) created", e.Object.GetNamespace(), e.Object.GetName())
	if !util.CheckIngressClassMatched(h.ctx, h.client, e.Object, h.ingClassName) {
		return
	}
	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: e.Object.GetNamespace(),
		Name:      e.Object.GetName(),
	}})
}

func (h *multiClusterIngressEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*networkingv1alpha1.MultiClusterIngress], queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	mciOld := e.ObjectOld
	mciNew := e.ObjectNew
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

func (h *multiClusterIngressEventHandler) Delete(_ context.Context, _ event.TypedDeleteEvent[*networkingv1alpha1.MultiClusterIngress], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// Since finalizer is added to the multiClusterIngress object,
	// the delete event is processed by the update event.
}

func (h *multiClusterIngressEventHandler) Generic(_ context.Context, e event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress], queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if !util.CheckIngressClassMatched(h.ctx, h.client, e.Object, h.ingClassName) {
		return
	}
	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: e.Object.GetNamespace(),
		Name:      e.Object.GetName(),
	}})
}

func newServiceEventHandler(mciEventChan chan<- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress], client client.Client) handler.TypedEventHandler[*corev1.Service, reconcile.Request] {
	return &serviceEventHandler{
		mciEventChan: mciEventChan,
		client:       client,
	}
}

var _ handler.TypedEventHandler[*corev1.Service, reconcile.Request] = (*serviceEventHandler)(nil)

type serviceEventHandler struct {
	mciEventChan chan<- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress]
	client       client.Client
}

func (h *serviceEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	klog.Infof("service(%s/%s) created", e.Object.GetNamespace(), e.Object.GetName())
	h.enqueueImpactedMCI(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	svcOld := e.ObjectOld
	svcNew := e.ObjectNew

	// We only care about the update events below:
	if equality.Semantic.DeepEqual(svcOld.Annotations, svcNew.Annotations) &&
		equality.Semantic.DeepEqual(svcOld.Spec, svcNew.Spec) &&
		equality.Semantic.DeepEqual(svcOld.DeletionTimestamp.IsZero(), svcNew.DeletionTimestamp.IsZero()) {
		return
	}

	h.enqueueImpactedMCI(svcNew.Namespace, svcNew.Name)
}

func (h *serviceEventHandler) Delete(_ context.Context, e event.TypedDeleteEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueImpactedMCI(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *serviceEventHandler) Generic(_ context.Context, e event.TypedGenericEvent[*corev1.Service], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
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
		h.mciEventChan <- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress]{
			Object: mci,
		}
	}
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
	klog.Infof("eps(%s/%s) created", e.Object.GetNamespace(), e.Object.GetName())
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

func newSecretEventHandler(mciEventChan chan<- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress], client client.Client) handler.TypedEventHandler[*corev1.Secret, reconcile.Request] {
	return &secretEventHandler{
		mciEventChan: mciEventChan,
		client:       client,
	}
}

var _ handler.TypedEventHandler[*corev1.Secret, reconcile.Request] = (*secretEventHandler)(nil)

type secretEventHandler struct {
	mciEventChan chan<- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress]
	client       client.Client
}

func (h *secretEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*corev1.Secret], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	klog.Infof("secret(%s/%s) created", e.Object.GetNamespace(), e.Object.GetName())
	h.enqueueImpactedMCI(e.Object.GetNamespace(), e.Object.GetName())
}

func (h *secretEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*corev1.Secret], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	secretOld := e.ObjectOld
	secretNew := e.ObjectNew

	if equality.Semantic.DeepEqual(secretOld.Annotations, secretNew.Annotations) &&
		equality.Semantic.DeepEqual(secretOld.Data, secretNew.Data) &&
		equality.Semantic.DeepEqual(secretOld.StringData, secretNew.StringData) {
		return
	}

	h.enqueueImpactedMCI(secretNew.Namespace, secretNew.Name)
}

func (h *secretEventHandler) Delete(_ context.Context, e event.TypedDeleteEvent[*corev1.Secret], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
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
		h.mciEventChan <- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress]{
			Object: mci,
		}
	}
}

func (h *secretEventHandler) Generic(_ context.Context, _ event.TypedGenericEvent[*corev1.Secret], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {

}

func newClusterEventHandler(mciEventChan chan<- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress], client client.Client) handler.TypedEventHandler[*clusterv1alpha1.Cluster, reconcile.Request] {
	return &clusterEventHandler{
		client:       client,
		mciEventChan: mciEventChan,
	}
}

var _ handler.TypedEventHandler[*clusterv1alpha1.Cluster, reconcile.Request] = (*clusterEventHandler)(nil)

type clusterEventHandler struct {
	client       client.Client
	mciEventChan chan<- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress]
}

func (h *clusterEventHandler) Create(_ context.Context, _ event.TypedCreateEvent[*clusterv1alpha1.Cluster], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *clusterEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*clusterv1alpha1.Cluster], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	oldCluster := e.ObjectOld
	newCluster := e.ObjectNew
	oldExist, newExist := false, false
	for _, action := range oldCluster.Status.RemedyActions {
		if action == string(remedyv1alpha1.TrafficControl) {
			oldExist = true
			break
		}
	}
	for _, action := range newCluster.Status.RemedyActions {
		if action == string(remedyv1alpha1.TrafficControl) {
			newExist = true
			break
		}
	}

	if oldExist == newExist {
		return
	}

	mciList := &networkingv1alpha1.MultiClusterIngressList{}
	if err := h.client.List(context.Background(), mciList); err != nil {
		klog.Errorf("failed to fetch multiclusteringresses")
		return
	}

	for index := range mciList.Items {
		mci := &mciList.Items[index]
		if !mciSvcLocationsContainsCluster(mci, newCluster) {
			continue
		}
		h.mciEventChan <- event.TypedGenericEvent[*networkingv1alpha1.MultiClusterIngress]{
			Object: mci,
		}
	}
}

func (h *clusterEventHandler) Delete(_ context.Context, _ event.TypedDeleteEvent[*clusterv1alpha1.Cluster], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *clusterEventHandler) Generic(_ context.Context, _ event.TypedGenericEvent[*clusterv1alpha1.Cluster], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func mciSvcLocationsContainsCluster(mci *networkingv1alpha1.MultiClusterIngress, cluster *clusterv1alpha1.Cluster) bool {
	for _, location := range mci.Status.ServiceLocations {
		for _, clusterName := range location.Clusters {
			if clusterName == cluster.Name {
				return true
			}
		}
	}
	return false
}
