package httpapi

import (
	"net/http"
	"strconv"

	"github.com/btrvodka/redigate/internal/service"
)

func (s *Server) clusterRoutes(mux *http.ServeMux) {
	//	@Summary		Cluster info
	//	@Description	CLUSTER INFO, parsed.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/info [get]
	s.api(mux, "GET /api/v1/cluster/info", func(r *http.Request) (any, error) {
		return s.svc.ClusterInfo(r.Context(), r.URL.Query().Get("node"))
	})
	//	@Summary		Cluster nodes
	//	@Description	CLUSTER NODES parsed into service.ClusterNode objects.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/nodes [get]
	s.api(mux, "GET /api/v1/cluster/nodes", func(r *http.Request) (any, error) {
		return s.svc.ClusterNodes(r.Context(), r.URL.Query().Get("node"))
	})
	//	@Summary		Cluster shards
	//	@Description	CLUSTER SHARDS.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/shards [get]
	s.api(mux, "GET /api/v1/cluster/shards", func(r *http.Request) (any, error) {
		return s.svc.ClusterShards(r.Context(), r.URL.Query().Get("node"))
	})
	//	@Summary		Cluster slots
	//	@Description	CLUSTER SLOTS.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/slots [get]
	s.api(mux, "GET /api/v1/cluster/slots", func(r *http.Request) (any, error) {
		return s.svc.ClusterSlots(r.Context(), r.URL.Query().Get("node"))
	})
	//	@Summary		Slot of a key
	//	@Description	The slot and its master according to CLUSTER NODES.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.KeySlotResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/keyslot [get]
	s.api(mux, "GET /api/v1/cluster/keyslot", func(r *http.Request) (any, error) {
		if !r.URL.Query().Has("key") {
			return nil, badRequest("key is required")
		}

		return s.svc.KeySlot(r.Context(), r.URL.Query().Get("key"))
	})
	//	@Summary		Keys of a slot
	//	@Description	CLUSTER COUNTKEYSINSLOT and GETKEYSINSLOT on the slot owner.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			slot	path		int	true	"Slot"
	//	@Param			count	query		int	false	"Keys to return, default 100"
	//	@Success		200		{object}	Response{result=service.SlotKeysResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/slots/{slot}/keys [get]
	s.api(mux, "GET /api/v1/cluster/slots/{slot}/keys", func(r *http.Request) (any, error) {
		slot, err := strconv.Atoi(r.PathValue("slot"))
		if err != nil {
			return nil, badRequest("invalid slot %q", r.PathValue("slot"))
		}

		count, err := queryInt(r, "count", 100) //nolint:mnd // default page
		if err != nil {
			return nil, err
		}

		return s.svc.SlotKeys(r.Context(), slot, count)
	})

	//	@Summary		Fail over
	//	@Description	CLUSTER FAILOVER on the replica given by node.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			request	body		ClusterFailoverRequest	true	"Request body"
	//	@Param			node	query		string					true	"Replica address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/failover [post]
	s.api(mux, "POST /api/v1/cluster/failover", writeOp(func(r *http.Request) (any, error) {
		var body ClusterFailoverRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.Failover(r.Context(), sel.Node, body.Mode)
		})
	}))
	//	@Summary		Add a node
	//	@Description	CLUSTER MEET on node (any master by default).
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			request	body		ClusterMeetRequest	true	"Request body"
	//	@Param			node	query		string				false	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/meet [post]
	s.api(mux, "POST /api/v1/cluster/meet", writeOp(func(r *http.Request) (any, error) {
		var body ClusterMeetRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.Meet(r.Context(), sel.Node, body.IP, body.Port, body.BusPort)
		})
	}))
	//	@Summary		Remove a node
	//	@Description	CLUSTER FORGET on every node.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			request	body		ClusterForgetRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/forget [post]
	s.api(mux, "POST /api/v1/cluster/forget", writeOp(func(r *http.Request) (any, error) {
		var body ClusterForgetRequest

		return withBody(r, &body, func(service.NodeSelector) (any, error) {
			return s.svc.Forget(r.Context(), body.NodeID)
		})
	}))
	//	@Summary		Make a node a replica
	//	@Description	CLUSTER REPLICATE on node.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			request	body		ClusterReplicateRequest	true	"Request body"
	//	@Param			node	query		string					true	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/replicate [post]
	s.api(mux, "POST /api/v1/cluster/replicate", writeOp(func(r *http.Request) (any, error) {
		var body ClusterReplicateRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.Replicate(r.Context(), sel.Node, body.MasterID)
		})
	}))
	//	@Summary		Reset a node
	//	@Description	CLUSTER RESET SOFT or HARD on node.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			request	body		ClusterResetRequest	true	"Request body"
	//	@Param			node	query		string				true	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/reset [post]
	s.api(mux, "POST /api/v1/cluster/reset", writeOp(func(r *http.Request) (any, error) {
		var body ClusterResetRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.ClusterReset(r.Context(), sel.Node, body.Hard)
		})
	}))

	for path, add := range map[string]bool{"addslots": true, "delslots": false} {
		//	@Summary		Assign or remove slots
		//	@Description	CLUSTER ADDSLOTS/DELSLOTS, or the *RANGE variants with ranges, on node.
		//	@Tags			cluster
		//	@Produce		json
		//	@Param			request	body		ClusterSlotsRequest	true	"Request body"
		//	@Param			node	query		string				true	"Node address"
		//	@Success		200		{object}	Response{result=service.CommandResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/cluster/addslots [post]
		//	@Router			/cluster/delslots [post]
		s.api(mux, "POST /api/v1/cluster/"+path, writeOp(func(r *http.Request) (any, error) {
			var body ClusterSlotsRequest

			return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
				return s.svc.ChangeSlots(r.Context(), sel.Node, add, body.Slots, body.Ranges)
			})
		}))
	}

	//	@Summary		Change a slot state
	//	@Description	CLUSTER SETSLOT on node: importing, migrating, stable or node.
	//	@Tags			cluster
	//	@Produce		json
	//	@Param			request	body		ClusterSetSlotRequest	true	"Request body"
	//	@Param			node	query		string					true	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cluster/setslot [post]
	s.api(mux, "POST /api/v1/cluster/setslot", writeOp(func(r *http.Request) (any, error) {
		var body ClusterSetSlotRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			if body.Slot == nil {
				return nil, badRequest("slot is required")
			}

			return s.svc.SetSlot(r.Context(), sel.Node, *body.Slot, body.State, body.NodeID)
		})
	}))
}

type ClusterFailoverRequest struct {
	// Failover mode, empty for a coordinated failover.
	Mode string `json:"mode" enums:"force,takeover"`
}

type ClusterMeetRequest struct {
	// IP address.
	IP string `json:"ip"`
	// Port.
	Port int `json:"port"`
	// Cluster bus port, port+10000 by default.
	BusPort int `json:"bus_port"`
}

type ClusterForgetRequest struct {
	// Cluster node id.
	NodeID string `json:"node_id"`
}

type ClusterReplicateRequest struct {
	// Master node id.
	MasterID string `json:"master_id"`
}

type ClusterResetRequest struct {
	// HARD reset: also forget the node id and epochs.
	Hard bool `json:"hard"`
}

type ClusterSlotsRequest struct {
	// Slots.
	Slots []int `json:"slots"`
	// Slot ranges [first, last], alternative to slots.
	Ranges [][2]int `json:"ranges"`
}

type ClusterSetSlotRequest struct {
	// Slot.
	Slot *int `json:"slot"`
	// Slot state.
	State string `json:"state" enums:"importing,migrating,stable,node"`
	// Node id for importing, migrating and node.
	NodeID string `json:"node_id"`
}
