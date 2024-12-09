#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
source "${REPO_ROOT}"/hack/util.sh

# step0: variable define
KARMADA_SYSTEM_NAMESPACE="karmada-system"
KUBECONFIG_PATH=${KUBECONFIG_PATH:-"${HOME}/.kube"}
MAIN_KUBECONFIG=${MAIN_KUBECONFIG:-"${KUBECONFIG_PATH}/karmada.config"}
HOST_CLUSTER_NAME=${HOST_CLUSTER_NAME:-"karmada-host"}
KARMADA_APISERVER=${KARMADA_APISERVER:-"karmada-apiserver"}

export KUBECONFIG="${MAIN_KUBECONFIG}"

# step1: make image
export VERSION="latest"
export REGISTRY="docker.io/karmada"
make image-multicluster-provider-fake GOOS="linux" --directory="${REPO_ROOT}"

# step2: load image
kind load docker-image "${REGISTRY}/multicluster-provider-fake:${VERSION}" --name="${HOST_CLUSTER_NAME}"

# step3: create multicluster-provider-config secret to access karmada-apiserver
kubectl --context="${HOST_CLUSTER_NAME}" create secret generic multicluster-provider-config --from-file=karmada.config="${MAIN_KUBECONFIG}" -n "${KARMADA_SYSTEM_NAMESPACE}"

# step4: deploy multicluster-provider-fake
kubectl --context="${HOST_CLUSTER_NAME}" apply -f "${REPO_ROOT}/artifacts/deploy/multicluster-provider-fake.yaml"
util::wait_pod_ready "${HOST_CLUSTER_NAME}" multicluster-provider-fake "${KARMADA_SYSTEM_NAMESPACE}"

# step5: deploy ingressclass-fake
kubectl --context="${KARMADA_APISERVER}" apply -f "${REPO_ROOT}/artifacts/deploy/ingressclass-fake.yaml"
