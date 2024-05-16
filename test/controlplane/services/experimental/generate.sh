#!/usr/bin/env bash
#
# Generate the golden test files for the experimental tests.
#

set -eux

dir=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )

. "${dir}/../../k8s_versions.sh"

export KUBECONFIG="${dir}/kubeconfig"

# Keep this lightweight by just generating test-cases for the latest k8s version
# we target.
version=${versions[$((${#versions[@]} - 1))]} # ahh bash

mkdir -p "${dir}/v${version}"

: Start a kind cluster
kind create cluster --config "${dir}/manifests/kind-config-${version}.yaml" --name experimental

: Wait for service account to be created
until kubectl get serviceaccount/default; do
    sleep 5
done

: Preloading images
kind load --name experimental docker-image "${cilium_container_repo}/${cilium_container_image}:${cilium_version}" || true
kind load --name experimental docker-image "${cilium_container_repo}/${cilium_operator_container_image}:${cilium_version}" || true || true

: Install cilium
cilium install --wait

: Dump the initial state
kubectl get nodes,services,endpoints,endpointslices -o yaml > "${dir}/v${version}/init.yaml"

: Apply the manifest
kubectl create namespace test
kubectl apply -f "${dir}/manifests/services.yaml"

: Wait for all pods to be running
kubectl wait -n test --for=condition=ready --timeout=10m --all pods

: Dump the services and endpoints
kubectl get -n test services,endpoints,endpointslices,pods -o yaml > "${dir}/v${version}/state1.yaml"

: Delete all deployments
kubectl delete -n test deployments/echo deployments/echo-hostport 

: Wait for termination
kubectl wait -n test --for=delete --timeout=10m --all pods

: Dump the services and endpoints
kubectl get -n test services,endpoints,endpointslices,pods -o yaml > "${dir}/v${version}/state2.yaml"

: Tear down the cluster
kind delete clusters experimental
rm -f "${KUBECONFIG}"
