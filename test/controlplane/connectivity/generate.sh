#!/usr/bin/env bash
#

set -eux

dir=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )

. "${dir}/../k8s_versions.sh"

export KUBECONFIG="${dir}/kubeconfig"

for version in ${versions[*]}; do
    mkdir -p "${dir}/v${version}"

    : Start a kind cluster
    kind create cluster --config "${dir}/../services/dualstack/manifests/kind-config-${version}.yaml" --name connectivity

    : Wait for service account to be created
    until kubectl get serviceaccount/default; do
        sleep 5
    done

    : Preloading images
    kind load --name connectivity docker-image "${cilium_container_repo}/${cilium_container_image}:${cilium_version}" || true
    kind load --name connectivity docker-image "${cilium_container_repo}/${cilium_operator_container_image}:${cilium_version}" || true || true

    : Install cilium
    cilium install --wait

    : Dump the initial state of the cluster
    kubectl get nodes,ciliumnodes,services,endpoints,endpointslices,ciliumendpoints -o yaml > "${dir}/v${version}/init.yaml"

    : Run the connectivity test
    cilium connectivity test

    : Dump the services and endpoints
    kubectl get -n cilium-test services,endpoints,endpointslices,pods,ciliumendpoints -o yaml > "${dir}/v${version}/state1.yaml"

    : Tear down the cluster
    kind delete clusters connectivity
    rm -f "${KUBECONFIG}"

done
