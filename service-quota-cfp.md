<!-- This file was not reviewed by a human before submission -->

# CFP: Cilium Service and HostPort quotas

## Summary

Add Cilium-specific `ResourceQuota` resources to limit how many Service ports
a namespace can request and how many HostPorts it can use per node. This
protects finite Cilium service IDs and BPF maps from being consumed
disproportionately by one namespace.

The feature has two independent limits:

* `cilium.io/service-ports`: requested Service port units per
  namespace.
* `cilium.io/hostports-per-node`: HostPort units per namespace on
  each node.

The names are illustrative pending API review.

## User-facing behavior

Users set limits with ordinary Kubernetes `ResourceQuota` objects. Service
usage is calculated from Service specs, so quota behavior is predictable before
realization. Each deducible `IP:port` pair counts once; dual-stack pairs count
for both families. Each NodePort counts once because its frontend addresses
depend on the nodes. Headless Services count zero. The quota is one aggregate
Service limit; it does not distinguish Service types.

When a Service would exceed its namespace limit, its Kubernetes object remains
stored, but Cilium does not allocate the requested Service ports. The operator
calculates usage and marks Services that exceed the limit in Service status;
agents do not write Service status. Per-agent metrics expose usage and
rejections. `ResourceQuota.spec.hard` is the configured limit; Cilium does not
write `ResourceQuota.status.used` in this proposal. Users can inspect the
limit with `kubectl describe resourcequota` and an affected Service with
`kubectl describe service`. Updating the Service to fit,
raising the limit, or deleting other Services can resolve it. Deletion releases
requested quota as soon as the operator observes it; it does not wait for
datapath cleanup.

HostPort usage is charged per node and namespace. Ports are deduplicated and
multiplied by applicable host IPs and address families. CNI `ADD` rejects a
Pod when that node's namespace budget would be exceeded. The Pod may already
exist in the API, but it will not start successfully on that node; the CNI
error should name the namespace, quota key, requested amount, current usage,
and limit. The order of CNI `ADD` requests determines which Pods fit. Retrying
after a Pod is removed, moved to a node with capacity, or after the limit is
raised can resolve the failure.

For HostPorts, the operator calculates the maximum usage on any single node
for that namespace by observing Pods and their assigned nodes. Per-agent
metrics expose local usage and rejections. Operator status and metrics may lag
the individual CNI decision. HostPort `ADD` enforcement remains strict on each
node even while the operator catches up.

## Operational experience

Quota exhaustion must be diagnosable without inspecting agent internals. Users
should be able to identify the configured limit with `kubectl describe
resourcequota`, and find an operator-written Service status condition or CNI
error describing what exceeded the limit. Per-agent metrics show local usage
and rejection counts; operator metrics show its aggregate calculation. Raising or deleting quota-consuming
resources should restore capacity automatically. Lowering a quota below
current usage does not evict or delete workloads; it prevents additional
Service allocations and HostPort CNI adds that would increase usage until
usage is within bounds. Agents expose per-agent quota usage and rejection
metrics, so operators can investigate temporary disagreement between agents.

Installation should provide generous default quotas for the Cilium namespace
so Cilium's own Services and Pods are protected without requiring users to
create a quota first. Users may change these defaults. Documentation should
explain the different enforcement timing, how each port unit
is counted, the per-node HostPort meaning, the status lag, and practical
resolution steps. Helm values and installation instructions should make clear
that ResourceQuota specifies the hard limit while Cilium reports usage through
Service status and metrics, and agents enforce allocations.

## Out of scope

This proposal covers Kubernetes Services and Pods observed by Cilium. It does
not include other load-balancer sources or separate quotas for Service types.

## Open design details

When a quota changes, agents may observe the new limit and Service updates in
different orders. Service enforcement is best-effort during that propagation;
agents expose metrics so divergence is visible. HostPort enforcement uses the
limit available to the local CNI path and remains strict per node. The
prototype should validate address-family counting and quota status behavior.
