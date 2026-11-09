#!/usr/bin/env bash
# This file was not reviewed by a human before submission
# SPDX-License-Identifier: Apache-2.0
# Copyright Authors of Cilium

# Build the current checkout and test Service port quota rejection and recovery.
# Usage: contrib/scripts/kind-smoketest-service-quotas.sh
# CLUSTER_NAME, TIMEOUT (seconds) and KEEP_CLUSTER=1 are optional.
# Successful runs delete the cluster unless KEEP_CLUSTER=1. Failed runs retain it.
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"
cluster_name=${CLUSTER_NAME:-service-quotas}
timeout=${TIMEOUT:-300}
namespace=service-quota-smoketest
quota_resource=cilium.io/service-ports
image_tag=service-quotas

for command in docker kind kubectl helm make python3; do
    command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }
done
[[ "$timeout" =~ ^[1-9][0-9]*$ ]] || { echo 'TIMEOUT must be a positive number of seconds' >&2; exit 1; }
docker info >/dev/null
if kind get clusters | grep -Fxq "$cluster_name"; then
    echo "Cluster $cluster_name already exists; choose another CLUSTER_NAME." >&2
    exit 1
fi

workdir=$(mktemp -d -t service-quotas.XXXXXX)
export KUBECONFIG="$workdir/kubeconfig"
cluster_created=false
cleanup() {
    status=$?
    trap - EXIT
    if [[ "$status" != 0 ]]; then
        kubectl -n "$namespace" get services -o wide >&2 || true
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

# Inspect agent-local state: API admission still accepts over-limit Services.
agent_shell() {
    kubectl -n kube-system exec "$agent" -c cilium-agent -- cilium-dbg shell -- "$@"
}
wait_for() {
    local deadline=$((SECONDS + timeout))
    until "$@"; do
        if (( SECONDS >= deadline )); then
            echo "Timed out waiting for: $*" >&2
            agent_shell db/show resource-quotas >&2 || true
            agent_shell db/show frontends >&2 || true
            exit 1
        fi
        sleep 2
    done
}
has_frontend() {
    agent_shell db/show --columns Address,ServiceName frontends \
        | grep -F "$1:$2/TCP" | grep -Fq "$namespace/$3"
}
quota_rejected() {
    agent_shell db/show --columns Component,Error health \
        | grep -F "$quota_resource" | grep -Fq "service/$namespace/$1"
}
quota_usage() {
    agent_shell db/get --columns Hard,Used resource-quotas "$namespace/$quota_resource" \
        | grep -Eq "^[[:space:]]*$1[[:space:]]+$2[[:space:]]*$"
}
set_limit() {
    # Mutate only the limit in the enforcing agent's test table, preserving
    # reservations. This exercises retry behavior without restarting the agent.
    agent_shell db/get --format=json resource-quotas "$namespace/$quota_resource" \
        | python3 -c '
import json, sys
quota = {key.lower(): value for key, value in json.load(sys.stdin).items()}
quota["hard"] = int(sys.argv[1])
quota["inputs"] = [{key.lower(): value for key, value in entry.items()} for entry in quota["inputs"]]
quota["inputs"][0]["hard"] = quota["hard"]
json.dump(quota, sys.stdout)
' "$1" \
        | kubectl -n kube-system exec -i "$agent" -c cilium-agent -- \
            sh -c 'cat > /tmp/service-quota-smoketest.json'
    agent_shell db/insert resource-quotas /tmp/service-quota-smoketest.json
}

kubectl -n "$namespace" create service clusterip accepted --tcp=80:80
first_ip=$(kubectl -n "$namespace" get service accepted -o jsonpath='{.spec.clusterIP}')
wait_for has_frontend "$first_ip" 80 accepted
wait_for quota_usage 1 1

kubectl -n "$namespace" patch service accepted --type=json \
    -p '[{"op":"add","path":"/spec/ports/-","value":{"name":"extra","port":81,"protocol":"TCP","targetPort":81}}]'
wait_for quota_rejected accepted
has_frontend "$first_ip" 80 accepted
if has_frontend "$first_ip" 81 accepted; then
    echo 'The over-limit update was realized' >&2
    exit 1
fi
quota_usage 1 1
echo 'PASS: the rejected update retained its old frontend and reservation.'

set_limit 2
wait_for has_frontend "$first_ip" 81 accepted
wait_for quota_usage 2 2
echo 'PASS: increasing quota automatically realized the pending update.'

set_limit 1
wait_for quota_usage 1 2
has_frontend "$first_ip" 80 accepted
has_frontend "$first_ip" 81 accepted
echo 'PASS: lowering quota preserved the existing allocation.'

kubectl -n "$namespace" create service clusterip pending --tcp=80:80
second_ip=$(kubectl -n "$namespace" get service pending -o jsonpath='{.spec.clusterIP}')
wait_for quota_rejected pending
if has_frontend "$second_ip" 80 pending; then
    echo 'The over-limit Service was realized' >&2
    exit 1
fi
echo 'PASS: the agent rejected a new Service while over quota.'

kubectl -n "$namespace" delete service accepted
wait_for has_frontend "$second_ip" 80 pending
wait_for quota_usage 1 1
echo 'PASS: deleting a Service freed capacity for the pending Service.'

kubectl -n "$namespace" delete service pending
wait_for quota_usage 1 0
echo 'PASS: deleting the final Service released all reserved units.'
