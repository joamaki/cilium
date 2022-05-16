package cmd

import (
	"go.uber.org/fx"

	"github.com/go-openapi/loads"

	"github.com/cilium/cilium/api/v1/server"
	"github.com/cilium/cilium/api/v1/server/restapi"
	"github.com/cilium/cilium/api/v1/server/restapi/daemon"
	"github.com/cilium/cilium/api/v1/server/restapi/endpoint"
	"github.com/cilium/cilium/api/v1/server/restapi/ipam"
	"github.com/cilium/cilium/api/v1/server/restapi/metrics"
	"github.com/cilium/cilium/api/v1/server/restapi/policy"
	"github.com/cilium/cilium/api/v1/server/restapi/prefilter"
	"github.com/cilium/cilium/api/v1/server/restapi/recorder"
	"github.com/cilium/cilium/api/v1/server/restapi/service"
	datapathOption "github.com/cilium/cilium/pkg/datapath/option"
	"github.com/cilium/cilium/pkg/option"
)

type APIHandlers struct {
	fx.In

	// PolicyDeletePolicyHandler sets the operation handler for the delete policy operation
	PolicyDeletePolicyHandler policy.DeletePolicyHandler
	// PrefilterDeletePrefilterHandler sets the operation handler for the delete prefilter operation
	PrefilterDeletePrefilterHandler prefilter.DeletePrefilterHandler
	// PrefilterGetPrefilterHandler sets the operation handler for the get prefilter operation
	PrefilterGetPrefilterHandler prefilter.GetPrefilterHandler
	// PrefilterPatchPrefilterHandler sets the operation handler for the patch prefilter operation
	PrefilterPatchPrefilterHandler prefilter.PatchPrefilterHandler

	// DaemonGetClusterNodesHandler sets the operation handler for the get cluster nodes operation
	DaemonGetClusterNodesHandler daemon.GetClusterNodesHandler
	// DaemonGetConfigHandler sets the operation handler for the get config operation
	DaemonGetConfigHandler daemon.GetConfigHandler
	// DaemonGetDebuginfoHandler sets the operation handler for the get debuginfo operation
	DaemonGetDebuginfoHandler daemon.GetDebuginfoHandler
	// DaemonGetHealthzHandler sets the operation handler for the get healthz operation
	DaemonGetHealthzHandler daemon.GetHealthzHandler
	// DaemonGetMapHandler sets the operation handler for the get map operation
	DaemonGetMapHandler daemon.GetMapHandler
	// DaemonGetMapNameHandler sets the operation handler for the get map name operation
	DaemonGetMapNameHandler daemon.GetMapNameHandler
	// DaemonPatchConfigHandler sets the operation handler for the patch config operation
	DaemonPatchConfigHandler daemon.PatchConfigHandler

	// EndpointGetEndpointHandler sets the operation handler for the get endpoint operation
	EndpointGetEndpointHandler endpoint.GetEndpointHandler
	// EndpointGetEndpointIDHandler sets the operation handler for the get endpoint ID operation
	EndpointGetEndpointIDHandler endpoint.GetEndpointIDHandler
	// EndpointGetEndpointIDConfigHandler sets the operation handler for the get endpoint ID config operation
	EndpointGetEndpointIDConfigHandler endpoint.GetEndpointIDConfigHandler
	// EndpointGetEndpointIDHealthzHandler sets the operation handler for the get endpoint ID healthz operation
	EndpointGetEndpointIDHealthzHandler endpoint.GetEndpointIDHealthzHandler
	// EndpointGetEndpointIDLabelsHandler sets the operation handler for the get endpoint ID labels operation
	EndpointGetEndpointIDLabelsHandler endpoint.GetEndpointIDLabelsHandler
	// EndpointGetEndpointIDLogHandler sets the operation handler for the get endpoint ID log operation
	EndpointGetEndpointIDLogHandler endpoint.GetEndpointIDLogHandler
	// EndpointDeleteEndpointIDHandler sets the operation handler for the delete endpoint ID operation
	EndpointDeleteEndpointIDHandler endpoint.DeleteEndpointIDHandler
	// EndpointPutEndpointIDHandler sets the operation handler for the put endpoint ID operation
	EndpointPutEndpointIDHandler endpoint.PutEndpointIDHandler
	// EndpointPatchEndpointIDHandler sets the operation handler for the patch endpoint ID operation
	EndpointPatchEndpointIDHandler endpoint.PatchEndpointIDHandler
	// EndpointPatchEndpointIDConfigHandler sets the operation handler for the patch endpoint ID config operation
	EndpointPatchEndpointIDConfigHandler endpoint.PatchEndpointIDConfigHandler
	// EndpointPatchEndpointIDLabelsHandler sets the operation handler for the patch endpoint ID labels operation
	EndpointPatchEndpointIDLabelsHandler endpoint.PatchEndpointIDLabelsHandler

	// PolicyGetFqdnCacheHandler sets the operation handler for the get fqdn cache operation
	PolicyGetFqdnCacheHandler policy.GetFqdnCacheHandler
	// PolicyGetFqdnCacheIDHandler sets the operation handler for the get fqdn cache ID operation
	PolicyGetFqdnCacheIDHandler policy.GetFqdnCacheIDHandler
	// PolicyGetFqdnNamesHandler sets the operation handler for the get fqdn names operation
	PolicyGetFqdnNamesHandler policy.GetFqdnNamesHandler
	// PolicyGetIPHandler sets the operation handler for the get IP operation
	PolicyGetIPHandler policy.GetIPHandler
	// PolicyGetIdentityHandler sets the operation handler for the get identity operation
	PolicyGetIdentityHandler policy.GetIdentityHandler
	// PolicyGetIdentityEndpointsHandler sets the operation handler for the get identity endpoints operation
	PolicyGetIdentityEndpointsHandler policy.GetIdentityEndpointsHandler
	// PolicyGetIdentityIDHandler sets the operation handler for the get identity ID operation
	PolicyGetIdentityIDHandler policy.GetIdentityIDHandler
	// PolicyPutPolicyHandler sets the operation handler for the put policy operation
	PolicyPutPolicyHandler policy.PutPolicyHandler
	// PolicyGetPolicyHandler sets the operation handler for the get policy operation
	PolicyGetPolicyHandler policy.GetPolicyHandler
	// PolicyGetPolicyResolveHandler sets the operation handler for the get policy resolve operation
	PolicyGetPolicyResolveHandler policy.GetPolicyResolveHandler
	// PolicyGetPolicySelectorsHandler sets the operation handler for the get policy selectors operation
	PolicyGetPolicySelectorsHandler policy.GetPolicySelectorsHandler
	// PolicyDeleteFqdnCacheHandler sets the operation handler for the delete fqdn cache operation
	PolicyDeleteFqdnCacheHandler policy.DeleteFqdnCacheHandler

	// ServiceGetLrpHandler sets the operation handler for the get lrp operation
	ServiceGetLrpHandler service.GetLrpHandler

	// ServiceDeleteServiceIDHandler sets the operation handler for the delete service ID operation
	ServiceDeleteServiceIDHandler service.DeleteServiceIDHandler
	// ServicePutServiceIDHandler sets the operation handler for the put service ID operation
	ServicePutServiceIDHandler service.PutServiceIDHandler
	// ServiceGetServiceHandler sets the operation handler for the get service operation
	ServiceGetServiceHandler service.GetServiceHandler
	// ServiceGetServiceIDHandler sets the operation handler for the get service ID operation
	ServiceGetServiceIDHandler service.GetServiceIDHandler

	// MetricsGetMetricsHandler sets the operation handler for the get metrics operation
	MetricsGetMetricsHandler metrics.GetMetricsHandler

	// RecorderGetRecorderHandler sets the operation handler for the get recorder operation
	RecorderGetRecorderHandler recorder.GetRecorderHandler
	// RecorderGetRecorderIDHandler sets the operation handler for the get recorder ID operation
	RecorderGetRecorderIDHandler recorder.GetRecorderIDHandler
	// RecorderGetRecorderMasksHandler sets the operation handler for the get recorder masks operation
	RecorderGetRecorderMasksHandler recorder.GetRecorderMasksHandler
	// RecorderPutRecorderIDHandler sets the operation handler for the put recorder ID operation
	RecorderPutRecorderIDHandler recorder.PutRecorderIDHandler
	// RecorderDeleteRecorderIDHandler sets the operation handler for the delete recorder ID operation
	RecorderDeleteRecorderIDHandler recorder.DeleteRecorderIDHandler

	// IpamPostIpamHandler sets the operation handler for the post ipam operation
	IpamPostIpamHandler ipam.PostIpamHandler
	// IpamPostIpamIPHandler sets the operation handler for the post ipam IP operation
	IpamPostIpamIPHandler ipam.PostIpamIPHandler
	// IpamDeleteIpamIPHandler sets the operation handler for the delete ipam IP operation
	IpamDeleteIpamIPHandler ipam.DeleteIpamIPHandler
}

func apiProvider(h APIHandlers) *restapi.CiliumAPIAPI {

	swaggerSpec, err := loads.Analyzed(server.SwaggerJSON, "")
	if err != nil {
		log.WithError(err).Fatal("Cannot load swagger spec")
	}

	log.Info("Initializing Cilium API")
	restAPI := restapi.NewCiliumAPIAPI(swaggerSpec)

	restAPI.Logger = log.Infof

	// /healthz/
	restAPI.DaemonGetHealthzHandler = h.DaemonGetHealthzHandler

	// /cluster/nodes
	restAPI.DaemonGetClusterNodesHandler = h.DaemonGetClusterNodesHandler

	// /config/
	restAPI.DaemonGetConfigHandler = h.DaemonGetConfigHandler
	restAPI.DaemonPatchConfigHandler = h.DaemonPatchConfigHandler

	if option.Config.DatapathMode != datapathOption.DatapathModeLBOnly {
		// /endpoint/
		restAPI.EndpointGetEndpointHandler = h.EndpointGetEndpointHandler

		// /endpoint/{id}
		restAPI.EndpointGetEndpointIDHandler = h.EndpointGetEndpointIDHandler
		restAPI.EndpointPutEndpointIDHandler = h.EndpointPutEndpointIDHandler
		restAPI.EndpointPatchEndpointIDHandler = h.EndpointPatchEndpointIDHandler
		restAPI.EndpointDeleteEndpointIDHandler = h.EndpointDeleteEndpointIDHandler

		// /endpoint/{id}config/
		restAPI.EndpointGetEndpointIDConfigHandler = h.EndpointGetEndpointIDConfigHandler
		restAPI.EndpointPatchEndpointIDConfigHandler = h.EndpointPatchEndpointIDConfigHandler

		// /endpoint/{id}/labels/
		restAPI.EndpointGetEndpointIDLabelsHandler = h.EndpointGetEndpointIDLabelsHandler
		restAPI.EndpointPatchEndpointIDLabelsHandler = h.EndpointPatchEndpointIDLabelsHandler

		// /endpoint/{id}/log/
		restAPI.EndpointGetEndpointIDLogHandler = h.EndpointGetEndpointIDLogHandler

		// /endpoint/{id}/healthz
		restAPI.EndpointGetEndpointIDHealthzHandler = h.EndpointGetEndpointIDHealthzHandler

		// /identity/
		restAPI.PolicyGetIdentityHandler = h.PolicyGetIdentityHandler
		restAPI.PolicyGetIdentityIDHandler = h.PolicyGetIdentityIDHandler

		// /identity/endpoints
		restAPI.PolicyGetIdentityEndpointsHandler = h.PolicyGetIdentityEndpointsHandler

		// /policy/
		restAPI.PolicyGetPolicyHandler = h.PolicyGetPolicyHandler
		restAPI.PolicyPutPolicyHandler = h.PolicyPutPolicyHandler
		restAPI.PolicyDeletePolicyHandler = h.PolicyDeletePolicyHandler
		restAPI.PolicyGetPolicySelectorsHandler = h.PolicyGetPolicySelectorsHandler

		// /policy/resolve/
		restAPI.PolicyGetPolicyResolveHandler = h.PolicyGetPolicyResolveHandler

		// /lrp/
		restAPI.ServiceGetLrpHandler = h.ServiceGetLrpHandler
	}

	// /service/{id}/
	restAPI.ServiceGetServiceIDHandler = h.ServiceGetServiceIDHandler
	restAPI.ServiceDeleteServiceIDHandler = h.ServiceDeleteServiceIDHandler
	restAPI.ServicePutServiceIDHandler = h.ServicePutServiceIDHandler

	// /service/
	restAPI.ServiceGetServiceHandler = h.ServiceGetServiceHandler

	// /recorder/{id}/
	restAPI.RecorderGetRecorderIDHandler = h.RecorderGetRecorderIDHandler
	restAPI.RecorderDeleteRecorderIDHandler = h.RecorderDeleteRecorderIDHandler
	restAPI.RecorderPutRecorderIDHandler = h.RecorderPutRecorderIDHandler

	// /recorder/
	restAPI.RecorderGetRecorderHandler = h.RecorderGetRecorderHandler

	// /recorder/masks
	restAPI.RecorderGetRecorderMasksHandler = h.RecorderGetRecorderMasksHandler

	// /prefilter/
	restAPI.PrefilterGetPrefilterHandler = h.PrefilterGetPrefilterHandler
	restAPI.PrefilterDeletePrefilterHandler = h.PrefilterDeletePrefilterHandler
	restAPI.PrefilterPatchPrefilterHandler = h.PrefilterPatchPrefilterHandler

	if option.Config.DatapathMode != datapathOption.DatapathModeLBOnly {
		// /ipam/{ip}/
		restAPI.IpamPostIpamHandler = h.IpamPostIpamHandler
		restAPI.IpamPostIpamIPHandler = h.IpamPostIpamIPHandler
		restAPI.IpamDeleteIpamIPHandler = h.IpamDeleteIpamIPHandler
	}

	// /debuginfo
	restAPI.DaemonGetDebuginfoHandler = h.DaemonGetDebuginfoHandler

	// /map
	restAPI.DaemonGetMapHandler = h.DaemonGetMapHandler
	restAPI.DaemonGetMapNameHandler = h.DaemonGetMapNameHandler

	// metrics
	restAPI.MetricsGetMetricsHandler = h.MetricsGetMetricsHandler

	if option.Config.DatapathMode != datapathOption.DatapathModeLBOnly {
		// /fqdn/cache
		restAPI.PolicyGetFqdnCacheHandler = h.PolicyGetFqdnCacheHandler
		restAPI.PolicyDeleteFqdnCacheHandler = h.PolicyDeleteFqdnCacheHandler
		restAPI.PolicyGetFqdnCacheIDHandler = h.PolicyGetFqdnCacheIDHandler
		restAPI.PolicyGetFqdnNamesHandler = h.PolicyGetFqdnNamesHandler
	}

	// /ip/
	restAPI.PolicyGetIPHandler = h.PolicyGetIPHandler

	return restAPI
}

// UnconvertedAPIHandlers are the API handlers that are still instantiated here in the daemon package.
type UnconvertedAPIHandlers struct {
	fx.Out

	// PolicyDeletePolicyHandler sets the operation handler for the delete policy operation
	PolicyDeletePolicyHandler policy.DeletePolicyHandler
	// PrefilterDeletePrefilterHandler sets the operation handler for the delete prefilter operation
	PrefilterDeletePrefilterHandler prefilter.DeletePrefilterHandler
	// PrefilterGetPrefilterHandler sets the operation handler for the get prefilter operation
	PrefilterGetPrefilterHandler prefilter.GetPrefilterHandler
	// PrefilterPatchPrefilterHandler sets the operation handler for the patch prefilter operation
	PrefilterPatchPrefilterHandler prefilter.PatchPrefilterHandler

	// DaemonGetClusterNodesHandler sets the operation handler for the get cluster nodes operation
	DaemonGetClusterNodesHandler daemon.GetClusterNodesHandler
	// DaemonGetConfigHandler sets the operation handler for the get config operation
	DaemonGetConfigHandler daemon.GetConfigHandler
	// DaemonGetDebuginfoHandler sets the operation handler for the get debuginfo operation
	DaemonGetDebuginfoHandler daemon.GetDebuginfoHandler
	// DaemonGetHealthzHandler sets the operation handler for the get healthz operation
	DaemonGetHealthzHandler daemon.GetHealthzHandler
	// DaemonGetMapHandler sets the operation handler for the get map operation
	DaemonGetMapHandler daemon.GetMapHandler
	// DaemonGetMapNameHandler sets the operation handler for the get map name operation
	DaemonGetMapNameHandler daemon.GetMapNameHandler
	// DaemonPatchConfigHandler sets the operation handler for the patch config operation
	DaemonPatchConfigHandler daemon.PatchConfigHandler

	// EndpointGetEndpointHandler sets the operation handler for the get endpoint operation
	EndpointGetEndpointHandler endpoint.GetEndpointHandler
	// EndpointGetEndpointIDHandler sets the operation handler for the get endpoint ID operation
	EndpointGetEndpointIDHandler endpoint.GetEndpointIDHandler
	// EndpointGetEndpointIDConfigHandler sets the operation handler for the get endpoint ID config operation
	EndpointGetEndpointIDConfigHandler endpoint.GetEndpointIDConfigHandler
	// EndpointGetEndpointIDHealthzHandler sets the operation handler for the get endpoint ID healthz operation
	EndpointGetEndpointIDHealthzHandler endpoint.GetEndpointIDHealthzHandler
	// EndpointGetEndpointIDLabelsHandler sets the operation handler for the get endpoint ID labels operation
	EndpointGetEndpointIDLabelsHandler endpoint.GetEndpointIDLabelsHandler
	// EndpointGetEndpointIDLogHandler sets the operation handler for the get endpoint ID log operation
	EndpointGetEndpointIDLogHandler endpoint.GetEndpointIDLogHandler
	// EndpointDeleteEndpointIDHandler sets the operation handler for the delete endpoint ID operation
	EndpointDeleteEndpointIDHandler endpoint.DeleteEndpointIDHandler
	// EndpointPutEndpointIDHandler sets the operation handler for the put endpoint ID operation
	EndpointPutEndpointIDHandler endpoint.PutEndpointIDHandler
	// EndpointPatchEndpointIDHandler sets the operation handler for the patch endpoint ID operation
	EndpointPatchEndpointIDHandler endpoint.PatchEndpointIDHandler
	// EndpointPatchEndpointIDConfigHandler sets the operation handler for the patch endpoint ID config operation
	EndpointPatchEndpointIDConfigHandler endpoint.PatchEndpointIDConfigHandler
	// EndpointPatchEndpointIDLabelsHandler sets the operation handler for the patch endpoint ID labels operation
	EndpointPatchEndpointIDLabelsHandler endpoint.PatchEndpointIDLabelsHandler

	// PolicyGetFqdnCacheHandler sets the operation handler for the get fqdn cache operation
	PolicyGetFqdnCacheHandler policy.GetFqdnCacheHandler
	// PolicyGetFqdnCacheIDHandler sets the operation handler for the get fqdn cache ID operation
	PolicyGetFqdnCacheIDHandler policy.GetFqdnCacheIDHandler
	// PolicyGetFqdnNamesHandler sets the operation handler for the get fqdn names operation
	PolicyGetFqdnNamesHandler policy.GetFqdnNamesHandler
	// PolicyGetIdentityHandler sets the operation handler for the get identity operation
	PolicyGetIdentityHandler policy.GetIdentityHandler
	// PolicyGetIdentityEndpointsHandler sets the operation handler for the get identity endpoints operation
	PolicyGetIdentityEndpointsHandler policy.GetIdentityEndpointsHandler
	// PolicyGetIdentityIDHandler sets the operation handler for the get identity ID operation
	PolicyGetIdentityIDHandler policy.GetIdentityIDHandler
	// PolicyPutPolicyHandler sets the operation handler for the put policy operation
	PolicyPutPolicyHandler policy.PutPolicyHandler
	// PolicyGetPolicyHandler sets the operation handler for the get policy operation
	PolicyGetPolicyHandler policy.GetPolicyHandler
	// PolicyGetPolicyResolveHandler sets the operation handler for the get policy resolve operation
	PolicyGetPolicyResolveHandler policy.GetPolicyResolveHandler
	// PolicyGetPolicySelectorsHandler sets the operation handler for the get policy selectors operation
	PolicyGetPolicySelectorsHandler policy.GetPolicySelectorsHandler
	// PolicyDeleteFqdnCacheHandler sets the operation handler for the delete fqdn cache operation
	PolicyDeleteFqdnCacheHandler policy.DeleteFqdnCacheHandler

	// ServiceGetLrpHandler sets the operation handler for the get lrp operation
	ServiceGetLrpHandler service.GetLrpHandler

	// MetricsGetMetricsHandler sets the operation handler for the get metrics operation
	MetricsGetMetricsHandler metrics.GetMetricsHandler

	// RecorderGetRecorderHandler sets the operation handler for the get recorder operation
	RecorderGetRecorderHandler recorder.GetRecorderHandler
	// RecorderGetRecorderIDHandler sets the operation handler for the get recorder ID operation
	RecorderGetRecorderIDHandler recorder.GetRecorderIDHandler
	// RecorderGetRecorderMasksHandler sets the operation handler for the get recorder masks operation
	RecorderGetRecorderMasksHandler recorder.GetRecorderMasksHandler
	// RecorderPutRecorderIDHandler sets the operation handler for the put recorder ID operation
	RecorderPutRecorderIDHandler recorder.PutRecorderIDHandler
	// RecorderDeleteRecorderIDHandler sets the operation handler for the delete recorder ID operation
	RecorderDeleteRecorderIDHandler recorder.DeleteRecorderIDHandler

	// IpamPostIpamHandler sets the operation handler for the post ipam operation
	IpamPostIpamHandler ipam.PostIpamHandler
	// IpamPostIpamIPHandler sets the operation handler for the post ipam IP operation
	IpamPostIpamIPHandler ipam.PostIpamIPHandler
	// IpamDeleteIpamIPHandler sets the operation handler for the delete ipam IP operation
	IpamDeleteIpamIPHandler ipam.DeleteIpamIPHandler
}

func apiHandlerBridge(d *Daemon) UnconvertedAPIHandlers {
	var h UnconvertedAPIHandlers

	// /healthz/
	h.DaemonGetHealthzHandler = NewGetHealthzHandler(d)

	// /cluster/nodes
	h.DaemonGetClusterNodesHandler = NewGetClusterNodesHandler(d)

	// /config/
	h.DaemonGetConfigHandler = NewGetConfigHandler(d)
	h.DaemonPatchConfigHandler = NewPatchConfigHandler(d)

	if option.Config.DatapathMode != datapathOption.DatapathModeLBOnly {
		// /endpoint/
		h.EndpointGetEndpointHandler = NewGetEndpointHandler(d)

		// /endpoint/{id}
		h.EndpointGetEndpointIDHandler = NewGetEndpointIDHandler(d)
		h.EndpointPutEndpointIDHandler = NewPutEndpointIDHandler(d)
		h.EndpointPatchEndpointIDHandler = NewPatchEndpointIDHandler(d)
		h.EndpointDeleteEndpointIDHandler = NewDeleteEndpointIDHandler(d)

		// /endpoint/{id}config/
		h.EndpointGetEndpointIDConfigHandler = NewGetEndpointIDConfigHandler(d)
		h.EndpointPatchEndpointIDConfigHandler = NewPatchEndpointIDConfigHandler(d)

		// /endpoint/{id}/labels/
		h.EndpointGetEndpointIDLabelsHandler = NewGetEndpointIDLabelsHandler(d)
		h.EndpointPatchEndpointIDLabelsHandler = NewPatchEndpointIDLabelsHandler(d)

		// /endpoint/{id}/log/
		h.EndpointGetEndpointIDLogHandler = NewGetEndpointIDLogHandler(d)

		// /endpoint/{id}/healthz
		h.EndpointGetEndpointIDHealthzHandler = NewGetEndpointIDHealthzHandler(d)

		// /identity/
		h.PolicyGetIdentityHandler = newGetIdentityHandler(d)
		h.PolicyGetIdentityIDHandler = newGetIdentityIDHandler(d.identityAllocator)

		// /identity/endpoints
		h.PolicyGetIdentityEndpointsHandler = newGetIdentityEndpointsIDHandler(d)

		// /policy/
		h.PolicyGetPolicyHandler = newGetPolicyHandler(d.policy)
		h.PolicyPutPolicyHandler = newPutPolicyHandler(d)
		h.PolicyDeletePolicyHandler = newDeletePolicyHandler(d)
		h.PolicyGetPolicySelectorsHandler = newGetPolicyCacheHandler(d)

		// /policy/resolve/
		h.PolicyGetPolicyResolveHandler = NewGetPolicyResolveHandler(d)

		// /lrp/
		h.ServiceGetLrpHandler = NewGetLrpHandler(d.redirectPolicyManager)
	}

	// /recorder/{id}/
	h.RecorderGetRecorderIDHandler = NewGetRecorderIDHandler(d.rec)
	h.RecorderDeleteRecorderIDHandler = NewDeleteRecorderIDHandler(d.rec)
	h.RecorderPutRecorderIDHandler = NewPutRecorderIDHandler(d.rec)

	// /recorder/
	h.RecorderGetRecorderHandler = NewGetRecorderHandler(d.rec)

	// /recorder/masks
	h.RecorderGetRecorderMasksHandler = NewGetRecorderMasksHandler(d.rec)

	// /prefilter/
	h.PrefilterGetPrefilterHandler = NewGetPrefilterHandler(d)
	h.PrefilterDeletePrefilterHandler = NewDeletePrefilterHandler(d)
	h.PrefilterPatchPrefilterHandler = NewPatchPrefilterHandler(d)

	if option.Config.DatapathMode != datapathOption.DatapathModeLBOnly {
		// /ipam/{ip}/
		h.IpamPostIpamHandler = NewPostIPAMHandler(d)
		h.IpamPostIpamIPHandler = NewPostIPAMIPHandler(d)
		h.IpamDeleteIpamIPHandler = NewDeleteIPAMIPHandler(d)
	}

	// /debuginfo
	h.DaemonGetDebuginfoHandler = NewGetDebugInfoHandler(d)

	// /map
	h.DaemonGetMapHandler = NewGetMapHandler(d)
	h.DaemonGetMapNameHandler = NewGetMapNameHandler(d)

	// metrics
	h.MetricsGetMetricsHandler = NewGetMetricsHandler(d)

	if option.Config.DatapathMode != datapathOption.DatapathModeLBOnly {
		// /fqdn/cache
		h.PolicyGetFqdnCacheHandler = NewGetFqdnCacheHandler(d)
		h.PolicyDeleteFqdnCacheHandler = NewDeleteFqdnCacheHandler(d)
		h.PolicyGetFqdnCacheIDHandler = NewGetFqdnCacheIDHandler(d)
		h.PolicyGetFqdnNamesHandler = NewGetFqdnNamesHandler(d)
	}

	return h
}
