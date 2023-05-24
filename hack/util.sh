#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

# This script holds common bash variables and utility functions.

PROVIDER_GO_PACKAGE="github.com/karmada-io/multicluster-cloud-provider"

KARMADA_TARGET_SOURCE=(
  multicluster-provider-fake=cmd/controller-manager
)

function util::get_target_source() {
  local target=$1
  for s in "${KARMADA_TARGET_SOURCE[@]}"; do
    if [[ "$s" == ${target}=* ]]; then
      echo "${s##${target}=}"
      return
    fi
  done
}

function util::version_ldflags() {
  # Git information
  GIT_VERSION=0000
  GIT_COMMIT_HASH=$(git rev-parse HEAD)
  if git_status=$(git status --porcelain 2>/dev/null) && [[ -z ${git_status} ]]; then
    GIT_TREESTATE="clean"
  else
    GIT_TREESTATE="dirty"
  fi
  BUILDDATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ')
  LDFLAGS="-X github.com/karmada-io/multicluster-cloud-provider/pkg/version.gitVersion=${GIT_VERSION} \
                        -X github.com/karmada-io/multicluster-cloud-provider/version.gitCommit=${GIT_COMMIT_HASH} \
                        -X github.com/karmada-io/multicluster-cloud-provider/pkg/version.gitTreeState=${GIT_TREESTATE} \
                        -X github.com/karmada-io/multicluster-cloud-provider/pkg/version.buildDate=${BUILDDATE}"
  echo $LDFLAGS
}

function util:host_platform() {
  echo "$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
}

# util::wait_pod_ready waits for pod state becomes ready until timeout.
# Parameters:
#  - $1: k8s context name, such as "karmada-apiserver"
#  - $2: pod label, such as "app=etcd"
#  - $3: pod namespace, such as "karmada-system"
#  - $4: time out, such as "200s"
function util::wait_pod_ready() {
    local context_name=$1
    local pod_label=$2
    local pod_namespace=$3

    echo "wait the $pod_label ready..."
    set +e
    util::kubectl_with_retry --context="$context_name" wait --for=condition=Ready --timeout=30s pods -l app=${pod_label} -n ${pod_namespace}
    ret=$?
    set -e
    if [ $ret -ne 0 ];then
      echo "kubectl describe info:"
      kubectl --context="$context_name" describe pod -l app=${pod_label} -n ${pod_namespace}
      echo "kubectl logs info:"
      kubectl --context="$context_name" logs -l app=${pod_label} -n ${pod_namespace}
    fi
    return ${ret}
}

# util::kubectl_with_retry will retry if execute kubectl command failed
# tolerate kubectl command failure that may happen before the pod is created by  StatefulSet/Deployment.
function util::kubectl_with_retry() {
    local ret=0
    for i in {1..10}; do
        kubectl "$@"
        ret=$?
        if [[ ${ret} -ne 0 ]]; then
            echo "kubectl $@ failed, retrying(${i} times)"
            sleep 1
            continue
        else
            return 0
        fi
    done

    echo "kubectl $@ failed"
    kubectl "$@"
    return ${ret}
}

function util::cmd_exist {
  local CMD=$(command -v ${1})
  if [[ ! -x ${CMD} ]]; then
    return 1
  fi
  return 0
}
