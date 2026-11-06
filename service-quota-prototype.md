<!-- This file was not reviewed by a human before submission -->

# Prototype plan: Cilium Service and HostPort quotas

## Goal

Prototype two `ResourceQuota` keys:

* `cilium.io/service-ports`: namespace-wide Service port usage, computed by
  the operator and enforced asynchronously by agents during Service
  realization.
* `cilium.io/hostports-per-node`: HostPort usage per namespace per node, enforced
  strictly by local CNI `ADD`.

Count only Kubernetes Services and Pods observed by Cilium. Other load
balancer sources do not count. Ship generous defaults for the Cilium
namespace, configurable through Helm values; no-quota behavior in other
namespaces remains unlimited unless a user creates a ResourceQuota.

## ResourceQuota behavior

Kubernetes represents quota hard and used values as resource-name-to-quantity
maps. This prototype uses `ResourceQuota.spec.hard` only as the configured
limit. It does not write Cilium usage into `ResourceQuota.status`; the
Kubernetes quota controller calculates usage through registered evaluators,
and Cilium's weighted Service and per-node Pod counts are not among them.

## Operator accounting and reporting

Add a focused operator controller watching ResourceQuotas, Services, and
Pods. It independently computes requested Service usage from Service specs
and HostPort usage from Pods and their assigned nodes; it does not consume
agent usage reports.

Service counting should be a pure function: count deduplicated `IP:port`
pairs implied by the Service, count both families for dual-stack, count each
NodePort once, and count headless Services as zero. Validate exact address
selection for each Service type against Cilium's realization code before
freezing the behavior.

For HostPorts, derive the same deduplicated host IP/family port units used by
the CNI check. The operator updates Service status only for Services it
determines are over quota. Do not have agents patch Service status. Avoid
rewriting status on every reconciliation when the condition and message have
not changed. Expose operator aggregate usage and per-agent local usage and
rejection counts through metrics, with bounded-cardinality labels.

## Agent Service enforcement

Consume the namespace hard limit and the operator's requested-usage view
through the existing agent/operator configuration mechanism if practical; if
not, identify the smallest new mechanism. In the K8s Service realization path
(`pkg/loadbalancer/reflectors/k8s.go` is the current reflector entry point),
compare the proposed Service's count against the observed namespace usage and
limit. Preserve an existing allocation if an update would exceed the quota;
do not tear down the old state while rejecting the new desired state. Log a
clear structured error and expose metrics for current usage, rejected Service
ports, and disagreement/decision counts. Use bounded-cardinality labels; do
not label metrics by Service name or namespace.

Service usage and quota changes can reach agents in different orders. Accept
best-effort realization during propagation for the prototype, but make the
decision inputs visible in per-agent metrics. The operator's aggregate
calculation represents requested spec usage, not datapath allocation success.

## CNI HostPort enforcement

Trace CNI `ADD`/`DEL` through the current agent call path and add a node-local,
atomic reservation keyed by namespace and Pod UID. `ADD` checks the node's
current quota and reserves before reporting success. Over-limit errors should
include requested units, current usage, and limit. Roll back reservations if
later `ADD` work fails; make retries idempotent. `DEL` releases idempotently.

The operator independently computes reported usage from scheduled Pods, so
agent-to-operator usage reporting is not needed. The local reservation state
must still be rebuilt after agent restart before accepting new `ADD`s, or the
strict per-node cap can be bypassed. Investigate whether Pod informer state is
sufficient to reconstruct reservations or whether durable local state is
needed. A lowered quota does not evict existing Pods; additional `ADD`s that
would increase usage are rejected until usage is below the new limit.

## Quota changes and defaults

When a quota is raised, agents should admit new Service allocations and
HostPort `ADD`s after observing the new value. When lowered below current
usage, existing Service allocations and running Pods remain; Service updates
that exceed the new budget are not realized, and new HostPort `ADD`s that
increase node usage are rejected. Operator status may transiently lag. Include
configurable generous default Service and HostPort limits in the Cilium
namespace quota manifest, and document how administrators tune those values.

## Iteration and validation

Keep the first prototype lightweight. Add small runnable scripts for the
agent and operator paths to iterate on counting, error messages, status
presentation, and quota changes before broadening test coverage. Then add
focused tests for the finalized counting and update behavior, including
Service update retention, CNI concurrent `ADD`, retry/rollback/`DEL`, restart
reconstruction, and quota reductions. Validate in a live cluster
following the repository development setup.

## Install and permissions

Add operator permissions to read Services, Pods, Nodes, and ResourceQuotas and
to update Service status. Add the narrowly required agent
permissions or config plumbing for reading quota limits. Verify chart defaults, install modes,
upgrade, and rollback with operator and agent version skew.
