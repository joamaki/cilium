#!/usr/bin/env bash
# This file was not reviewed by a human before submission
# SPDX-License-Identifier: Apache-2.0
# Copyright Authors of Cilium

# Build the current checkout and test HostPort quota rejection and recovery.
# Usage: contrib/scripts/kind-smoketest-hostport-quotas.sh
# CLUSTER_NAME, TIMEOUT (seconds), POD_IMAGE and KEEP_CLUSTER=1 are optional.
# Successful runs delete the cluster unless KEEP_CLUSTER=1. Failed runs retain it.
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"
cluster_name=${CLUSTER_NAME:-hostport-quotas}
timeout=${TIMEOUT:-300}
pod_image=${POD_IMAGE:-registry.k8s.io/pause:3.10}
namespace=hostport-quota-smoketest
quota_resource=cilium.io/hostports-per-node
image_tag=hostport-quotas

for command in docker kind kubectl helm make; do
    command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }
done
[[ "$timeout" =~ ^[1-9][0-9]*$ ]] || { echo 'TIMEOUT must be a positive number of seconds' >&2; exit 1; }
docker info >/dev/null
if kind get clusters | grep -Fxq "$cluster_name"; then
    echo "Cluster $cluster_name already exists; choose another CLUSTER_NAME." >&2
    exit 1
fi

workdir=$(mktemp -d -t hostport-quotas.XXXXXX)
export KUBECONFIG="$workdir/kubeconfig"
cluster_created=false
cleanup() {
    status=$?
    trap - EXIT
    if [[ "$status" != 0 ]]; then
        kubectl -n "$namespace" get pods -o wide >&2 || true
        kubectl -n "$namespace" get events --sort-by=.lastTimestamp >&2 || true
        kubectl -n kube-system logs -l k8s-app=cilium -c cilium-agent --tail=100 >&2 || true
    fi
    if [[ "$cluster_created" == true && "$status" == 0 && "${KEEP_CLUSTER:-0}" != 1 ]]; then
        kind delete cluster --name "$cluster_name"
        rm -rf -- "$workdir"
    else
        echo "Retained cluster/config for inspection: KUBECONFIG=$KUBECONFIG" >&2
    fi
    exit "$status"
}
trap cleanup EXIT

# Use the repository's documented Kind setup and build the modified agent/operator.
CLUSTER_NAME="$cluster_name" CONTROLPLANES=1 WORKERS=1 IPFAMILY=ipv4 KUBEPROXY_MODE=none make kind
cluster_created=true
make kind-image KIND_CLUSTER_NAME="$cluster_name" LOCAL_IMAGE_TAG="$image_tag" DOCKER_DEV_ACCOUNT=cilium
# With kube-proxy disabled, bootstrap against the API server's node address.
api_host=$(kubectl get node "$cluster_name-control-plane" \
    -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
helm upgrade --install cilium ./install/kubernetes/cilium --namespace kube-system \
    --set image.override="localhost:5000/cilium/cilium-dev:$image_tag" \
    --set image.pullPolicy=Never \
    --set operator.image.override="localhost:5000/cilium/operator-generic:$image_tag" \
    --set operator.image.pullPolicy=Never --set operator.image.suffix='' \
    --set operator.replicas=1 --set ipam.mode=kubernetes \
    --set k8sServiceHost="$api_host" --set k8sServicePort=6443 \
    --set ipv4.enabled=true --set ipv6.enabled=false --set kubeProxyReplacement=true \
    --set-json "ciliumResourceQuotas={\"$namespace:$quota_resource\":\"1\"}"
kubectl -n kube-system rollout status daemonset/cilium --timeout="${timeout}s"
kubectl -n kube-system rollout status deployment/cilium-operator --timeout="${timeout}s"
kubectl wait nodes --all --for=condition=Ready --timeout="${timeout}s"

node="$cluster_name-worker"
agent=$(kubectl -n kube-system get pods -l k8s-app=cilium \
    --field-selector "spec.nodeName=$node" -o jsonpath='{.items[0].metadata.name}')
kubectl create namespace "$namespace"
# Wait for the configured quota on the enforcing node.
deadline=$((SECONDS + timeout))
until kubectl -n kube-system exec "$agent" -c cilium-agent -- \
    cilium-dbg shell -- db/get resource-quotas "$namespace/$quota_resource" 2>/dev/null \
    | grep -Fq "api:config=1"; do
    if (( SECONDS >= deadline )); then
        echo 'Timed out waiting for configured quota' >&2
        exit 1
    fi
    sleep 2
done

create_pod() {
    local name=$1 port=$2
    kubectl -n "$namespace" apply -f - <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: $name
spec:
  nodeName: $node
  containers:
  - name: pause
    image: $pod_image
    ports:
    - containerPort: 8080
      hostPort: $port
      hostIP: 127.0.0.1
      protocol: TCP
YAML
}

# Explicit IPv4 host IPs cost one unit. Distinct ports avoid scheduler conflicts.
create_pod accepted 18080
kubectl -n "$namespace" wait pod/accepted --for=condition=Ready --timeout="${timeout}s"
create_pod rejected 18081
pod_uid=$(kubectl -n "$namespace" get pod rejected -o jsonpath='{.metadata.uid}')
deadline=$((SECONDS + timeout))
while true; do
    messages=$(kubectl -n "$namespace" get events \
        --field-selector "involvedObject.uid=$pod_uid,reason=FailedCreatePodSandBox" \
        -o jsonpath='{range .items[*]}{.message}{"\n"}{end}')
    if grep -F "$quota_resource" <<<"$messages" | grep -Fq 'quota exceeded'; then
        break
    fi
    if (( SECONDS >= deadline )); then
        echo 'Expected a HostPort quota rejection event, but none was reported' >&2
        exit 1
    fi
    sleep 2
done
[[ $(kubectl -n "$namespace" get pod rejected -o jsonpath='{.status.phase}') == Pending ]]
[[ -z $(kubectl -n "$namespace" get pod rejected -o jsonpath='{.status.podIP}') ]]
echo 'PASS: CNI ADD rejected the second Pod with the expected quota error.'

kubectl -n "$namespace" delete pod accepted --wait=true --timeout="${timeout}s"
kubectl -n "$namespace" wait pod/rejected --for=condition=Ready --timeout="${timeout}s"
echo 'PASS: deleting the accepted Pod released quota and the rejected Pod recovered.'
