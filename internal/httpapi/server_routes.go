package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/btrvodka/redigate/internal/auth"
	"github.com/btrvodka/redigate/internal/service"
)

func (s *Server) serverRoutes(mux *http.ServeMux) {
	//	@Summary		Server info
	//	@Description	INFO parsed into sections; key=value lists become objects.
	//	@Tags			server
	//	@Produce		json
	//	@Param			section	query		string	false	"Comma-separated sections"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/info [get]
	s.api(mux, "GET /api/v1/server/info", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.Info(r.Context(), sel, splitList(r.URL.Query().Get("section")))
		})
	})
	//	@Summary		Number of keys
	//	@Description	DBSIZE per node and the total over masters.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Param			db		query		int		false	"Database"
	//	@Success		200		{object}	Response{result=service.DBSizeResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/dbsize [get]
	s.api(mux, "GET /api/v1/server/dbsize", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.DBSize(r.Context(), sel)
		})
	})
	//	@Summary		Replication role
	//	@Description	ROLE.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/role [get]
	s.api(mux, "GET /api/v1/server/role", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.Role(r.Context(), sel)
		})
	})

	//	@Summary		Get configuration
	//	@Description	CONFIG GET.
	//	@Tags			server
	//	@Produce		json
	//	@Param			pattern	query		string	false	"Comma-separated patterns"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/config [get]
	s.api(mux, "GET /api/v1/server/config", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.ConfigGet(r.Context(), sel, splitList(r.URL.Query().Get("pattern")))
		})
	})
	//	@Summary		Set configuration
	//	@Description	CONFIG SET on every data node by default.
	//	@Tags			server
	//	@Produce		json
	//	@Param			body	ConfigSetRequest	true	"Request body"
	//	@Param			node	query		string				false	"Node address"
	//	@Param			target	query		string				false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/config [put]
	s.api(mux, "PUT /api/v1/server/config", writeOp(func(r *http.Request) (any, error) {
		var body ConfigSetRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.ConfigSet(r.Context(), sel, body.Params)
		})
	}))
	//	@Summary		Rewrite the config file
	//	@Description	CONFIG REWRITE.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/config/rewrite [post]
	s.api(mux, "POST /api/v1/server/config/rewrite", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.ConfigRewrite(r.Context(), sel)
		})
	}))
	//	@Summary		Reset statistics
	//	@Description	CONFIG RESETSTAT.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/config/resetstat [post]
	s.api(mux, "POST /api/v1/server/config/resetstat", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.ConfigResetStat(r.Context(), sel)
		})
	}))

	//	@Summary		Slow log
	//	@Description	SLOWLOG GET, parsed.
	//	@Tags			server
	//	@Produce		json
	//	@Param			count	query		int		false	"Entries, default 128"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/slowlog [get]
	s.api(mux, "GET /api/v1/server/slowlog", func(r *http.Request) (any, error) {
		count, err := queryInt(r, "count", 128) //nolint:mnd // redis default slowlog length
		if err != nil {
			return nil, err
		}

		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.Slowlog(r.Context(), sel, count)
		})
	})
	//	@Summary		Clear the slow log
	//	@Description	SLOWLOG RESET.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/slowlog [delete]
	s.api(mux, "DELETE /api/v1/server/slowlog", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.SlowlogReset(r.Context(), sel)
		})
	}))

	//	@Summary		Connected clients
	//	@Description	CLIENT LIST, parsed.
	//	@Tags			server
	//	@Produce		json
	//	@Param			type	query		string	false	"normal, master, replica or pubsub"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/clients [get]
	s.api(mux, "GET /api/v1/server/clients", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.ClientList(r.Context(), sel, r.URL.Query().Get("type"))
		})
	})
	//	@Summary		Kill clients
	//	@Description	CLIENT KILL with filters such as {"id": "42"} or {"user": "app"}.
	//	@Tags			server
	//	@Produce		json
	//	@Param			body	ClientKillRequest	true	"Request body"
	//	@Param			node	query		string				false	"Node address"
	//	@Param			target	query		string				false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/clients/kill [post]
	s.api(mux, "POST /api/v1/server/clients/kill", writeOp(func(r *http.Request) (any, error) {
		var body ClientKillRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.ClientKill(r.Context(), sel, body.Filters)
		})
	}))
	//	@Summary		Pause clients
	//	@Description	CLIENT PAUSE on masters by default.
	//	@Tags			server
	//	@Produce		json
	//	@Param			body	ClientPauseRequest	true	"Request body"
	//	@Param			node	query		string				false	"Node address"
	//	@Param			target	query		string				false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/clients/pause [post]
	s.api(mux, "POST /api/v1/server/clients/pause", writeOp(func(r *http.Request) (any, error) {
		var body ClientPauseRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.ClientPause(r.Context(), sel, body.TimeoutMS, body.Mode)
		})
	}))
	//	@Summary		Resume clients
	//	@Description	CLIENT UNPAUSE.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/clients/unpause [post]
	s.api(mux, "POST /api/v1/server/clients/unpause", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.ClientUnpause(r.Context(), sel)
		})
	}))

	//	@Summary		Memory statistics
	//	@Description	MEMORY STATS.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/memory [get]
	s.api(mux, "GET /api/v1/server/memory", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.MemoryStats(r.Context(), sel)
		})
	})
	//	@Summary		Memory report
	//	@Description	MEMORY DOCTOR.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/memory/doctor [get]
	s.api(mux, "GET /api/v1/server/memory/doctor", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.MemoryDoctor(r.Context(), sel)
		})
	})
	//	@Summary		Latency events
	//	@Description	LATENCY LATEST.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/latency [get]
	s.api(mux, "GET /api/v1/server/latency", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.LatencyLatest(r.Context(), sel)
		})
	})
	//	@Summary		Reset latency data
	//	@Description	LATENCY RESET.
	//	@Tags			server
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/latency [delete]
	s.api(mux, "DELETE /api/v1/server/latency", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.LatencyReset(r.Context(), sel)
		})
	}))

	//	@Summary		Command table
	//	@Description	Commands supported by the server with subcommands, flags and ACL categories.
	//	@Tags			server
	//	@Produce		json
	//	@Param			prefix	query		string	false	"Name prefix"
	//	@Success		200		{object}	Response{result=[]service.CommandInfo}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/commands [get]
	s.api(mux, "GET /api/v1/server/commands", func(r *http.Request) (any, error) {
		return s.svc.Commands(r.URL.Query().Get("prefix")), nil
	})
	//	@Summary		Command docs
	//	@Description	COMMAND DOCS.
	//	@Tags			server
	//	@Produce		json
	//	@Param			name	query		string	false	"Comma-separated commands"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/commands/docs [get]
	s.api(mux, "GET /api/v1/server/commands/docs", func(r *http.Request) (any, error) {
		return s.svc.CommandDocs(r.Context(), splitList(r.URL.Query().Get("name")))
	})
	//	@Summary		Modules and capabilities
	//	@Description	MODULE LIST and capabilities detected by command presence (json, search, timeseries, bloom, ...).
	//	@Tags			server
	//	@Produce		json
	//	@Success		200		{object}	Response{result=service.ModulesResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/modules [get]
	s.api(mux, "GET /api/v1/server/modules", func(r *http.Request) (any, error) {
		return s.svc.Modules(r.Context())
	})

	//	@Summary		Delete all keys
	//	@Description	FLUSHALL on every master.
	//	@Tags			server
	//	@Produce		json
	//	@Param			async	query		bool	false	"ASYNC"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/flushall [post]
	s.api(mux, "POST /api/v1/server/flushall", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.FlushAll(r.Context(), sel, r.URL.Query().Has("async"))
		})
	}))
	//	@Summary		Delete keys of a database
	//	@Description	FLUSHDB on every master.
	//	@Tags			server
	//	@Produce		json
	//	@Param			async	query		bool	false	"ASYNC"
	//	@Param			db		query		int		false	"Database"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/flushdb [post]
	s.api(mux, "POST /api/v1/server/flushdb", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.FlushDB(r.Context(), sel, r.URL.Query().Has("async"))
		})
	}))

	for _, cmd := range []string{"save", "bgsave", "bgrewriteaof"} {
		//	@Summary		Persist data
		//	@Description	SAVE, BGSAVE or BGREWRITEAOF on masters by default.
		//	@Tags			server
		//	@Produce		json
		//	@Param			node	query		string	false	"Node address"
		//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
		//	@Success		200		{object}	Response{result=service.FanOutResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/server/save [post]
		//	@Router			/server/bgsave [post]
		//	@Router			/server/bgrewriteaof [post]
		s.api(mux, "POST /api/v1/server/"+cmd, writeOp(func(r *http.Request) (any, error) {
			return withSelector(r, func(sel service.NodeSelector) (any, error) {
				return s.svc.Persist(r.Context(), sel, cmd)
			})
		}))
	}

	//	@Summary		Shut servers down
	//	@Description	SHUTDOWN; node or target is required.
	//	@Tags			server
	//	@Produce		json
	//	@Param			body	ShutdownRequest	true	"Request body"
	//	@Param			node	query		string			false	"Node address"
	//	@Param			target	query		string			false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/shutdown [post]
	s.api(mux, "POST /api/v1/server/shutdown", writeOp(func(r *http.Request) (any, error) {
		var body ShutdownRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.Shutdown(r.Context(), sel, service.ShutdownOptions(body))
		})
	}))
	//	@Summary		Change replication
	//	@Description	REPLICAOF host port, or REPLICAOF NO ONE with no_one; node is required.
	//	@Tags			server
	//	@Produce		json
	//	@Param			body	ReplicaOfRequest	true	"Request body"
	//	@Param			node	query		string				true	"Node address"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/server/replicaof [post]
	s.api(mux, "POST /api/v1/server/replicaof", writeOp(func(r *http.Request) (any, error) {
		var body ReplicaOfRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			if body.NoOne == (body.Host != "") {
				return nil, badRequest("either host and port or no_one=true is required")
			}

			return s.svc.ReplicaOf(r.Context(), sel, body.Host, body.Port)
		})
	}))
}

// writeOp rejects requests without write access.
func writeOp(h jsonHandler) jsonHandler {
	return func(r *http.Request) (any, error) {
		if accessFromContext(r.Context()) < auth.Full {
			return nil, newAPIError(http.StatusForbidden, CodeForbidden, "write access is required")
		}

		return h(r)
	}
}

// withSelector parses node, target and db query parameters.
func withSelector(r *http.Request, fn func(service.NodeSelector) (any, error)) (any, error) {
	query := r.URL.Query()
	sel := service.NodeSelector{Node: query.Get("node")}

	if query.Has("target") {
		target, err := service.ParseTarget(query.Get("target"))
		if err != nil {
			return nil, err //nolint:wrapcheck // validation error
		}

		sel.Target = target
	}

	if query.Has("db") {
		db, err := queryInt(r, "db", 0)
		if err != nil {
			return nil, err
		}

		sel.DB = &db
	}

	return fn(sel)
}

// withBody decodes an optional JSON body into v and parses the selector.
func withBody(r *http.Request, v any, fn func(service.NodeSelector) (any, error)) (any, error) {
	body, _, err := readBody(r)
	if err != nil {
		return nil, err
	}

	if len(strings.TrimSpace(string(body))) > 0 {
		if err := decodeJSON(body, v); err != nil {
			return nil, err
		}
	}

	return withSelector(r, fn)
}

func queryInt(r *http.Request, name string, def int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, badRequest("invalid %s %q", name, raw)
	}

	return value, nil
}

// splitList splits a comma-separated query value.
func splitList(s string) []string {
	var out []string

	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}

	return out
}

type ConfigSetRequest struct {
	// Parameters and values.
	Params map[string]string `json:"params"`
}

type ClientKillRequest struct {
	// CLIENT KILL filters: id, addr, laddr, user, type, skipme, maxage.
	Filters map[string]string `json:"filters"`
}

type ClientPauseRequest struct {
	// Pause duration in milliseconds.
	TimeoutMS int64 `json:"timeout_ms"`
	// Pause writes only or all commands.
	Mode string `json:"mode" enums:"write,all"`
}

type ShutdownRequest struct {
	// Save before shutting down or not.
	Save string `json:"save" enums:"save,nosave"`
	// Do not wait for lagging replicas.
	Now bool `json:"now"`
	// Ignore errors that prevent the shutdown.
	Force bool `json:"force"`
	// Cancel an ongoing shutdown.
	Abort bool `json:"abort"`
}

type ReplicaOfRequest struct {
	// Host.
	Host string `json:"host"`
	// Port.
	Port int `json:"port"`
	// Make the node a master.
	NoOne bool `json:"no_one"`
}
