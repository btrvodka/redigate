package httpapi

import (
	"net/http"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

func (s *Server) scriptRoutes(mux *http.ServeMux) {
	//	@Summary		Run a script
	//	@Description	EVAL or EVALSHA; read_only uses EVAL_RO/EVALSHA_RO. Keys route the call in cluster and must hash to the same slot.
	//	@Tags			scripts
	//	@Produce		json
	//	@Param			body	EvalRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/scripts/eval [post]
	s.api(mux, "POST /api/v1/scripts/eval", bodyRoute(s, func(_ *http.Request, b *EvalRequest) ([]any, func(any) any, error) {
		name, body := "eval", b.Script
		if b.SHA != "" {
			name, body = "evalsha", b.SHA
		}

		if (b.Script == "") == (b.SHA == "") {
			return nil, nil, badRequest("exactly one of script and sha is required")
		}

		if b.ReadOnly {
			name += "_ro"
		}

		return cmd(name, body, len(b.Keys), b.Keys, b.Args), nil, nil
	}))

	//	@Summary		Load a script
	//	@Description	SCRIPT LOAD on every data node, returns the sha per node.
	//	@Tags			scripts
	//	@Produce		json
	//	@Param			body	ScriptLoadRequest	true	"Request body"
	//	@Param			node	query		string				false	"Node address"
	//	@Param			target	query		string				false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/scripts/load [post]
	s.api(mux, "POST /api/v1/scripts/load", writeOp(func(r *http.Request) (any, error) {
		var body ScriptLoadRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.ScriptLoad(r.Context(), sel, body.Script)
		})
	}))
	//	@Summary		Check scripts
	//	@Description	SCRIPT EXISTS; everywhere tells whether every node has the script.
	//	@Tags			scripts
	//	@Produce		json
	//	@Param			sha		query		[]string	true	"SHA1 digests"	collectionFormat(multi)
	//	@Param			node	query		string		false	"Node address"
	//	@Param			target	query		string		false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.ScriptExistsResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/scripts/exists [get]
	s.api(mux, "GET /api/v1/scripts/exists", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.ScriptExists(r.Context(), sel, r.URL.Query()["sha"])
		})
	})
	//	@Summary		Flush scripts
	//	@Description	SCRIPT FLUSH.
	//	@Tags			scripts
	//	@Produce		json
	//	@Param			async	query		bool	false	"ASYNC"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/scripts/flush [post]
	s.api(mux, "POST /api/v1/scripts/flush", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.ScriptFlush(r.Context(), sel, r.URL.Query().Has("async"))
		})
	}))
	//	@Summary		Kill a script
	//	@Description	SCRIPT KILL.
	//	@Tags			scripts
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/scripts/kill [post]
	s.api(mux, "POST /api/v1/scripts/kill", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.ScriptKill(r.Context(), sel)
		})
	}))

	//	@Summary		List functions
	//	@Description	FUNCTION LIST per master.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			library		query		string	false	"Library name pattern"
	//	@Param			with_code	query		bool	false	"Include code"
	//	@Param			node		query		string	false	"Node address"
	//	@Param			target		query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200			{object}	Response{result=service.FanOutResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions [get]
	s.api(mux, "GET /api/v1/functions", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.FunctionList(r.Context(), sel, r.URL.Query().Get("library"), r.URL.Query().Has("with_code"))
		})
	})
	//	@Summary		Function stats
	//	@Description	FUNCTION STATS.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions/stats [get]
	s.api(mux, "GET /api/v1/functions/stats", func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.FunctionStats(r.Context(), sel)
		})
	})

	//	@Summary		Load a library
	//	@Description	FUNCTION LOAD on every master.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			body	FunctionLoadRequest	true	"Request body"
	//	@Param			node	query		string				false	"Node address"
	//	@Param			target	query		string				false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions [post]
	s.api(mux, "POST /api/v1/functions", writeOp(func(r *http.Request) (any, error) {
		var body FunctionLoadRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.FunctionLoad(r.Context(), sel, body.Code, body.Replace)
		})
	}))
	//	@Summary		Delete a library
	//	@Description	FUNCTION DELETE on every master.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			library	query		string	true	"Library"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions [delete]
	s.api(mux, "DELETE /api/v1/functions", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.FunctionDelete(r.Context(), sel, r.URL.Query().Get("library"))
		})
	}))
	//	@Summary		Delete all libraries
	//	@Description	FUNCTION FLUSH.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			async	query		bool	false	"ASYNC"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions/flush [post]
	s.api(mux, "POST /api/v1/functions/flush", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.FunctionFlush(r.Context(), sel, r.URL.Query().Has("async"))
		})
	}))
	//	@Summary		Kill a function
	//	@Description	FUNCTION KILL.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions/kill [post]
	s.api(mux, "POST /api/v1/functions/kill", writeOp(func(r *http.Request) (any, error) {
		return withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.FunctionKill(r.Context(), sel)
		})
	}))
	//	@Summary		Dump libraries
	//	@Description	FUNCTION DUMP of one master, base64.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions/dump [get]
	s.api(mux, "GET /api/v1/functions/dump", func(r *http.Request) (any, error) {
		return s.svc.FunctionDump(r.Context(), r.URL.Query().Get("node"))
	})

	//	@Summary		Restore libraries
	//	@Description	FUNCTION RESTORE on every master; policy is append, replace or flush.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			body	FunctionRestoreRequest	true	"Request body"
	//	@Param			node	query		string					false	"Node address"
	//	@Param			target	query		string					false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions/restore [post]
	s.api(mux, "POST /api/v1/functions/restore", writeOp(func(r *http.Request) (any, error) {
		var body FunctionRestoreRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.FunctionRestore(r.Context(), sel, []byte(body.Dump), body.Policy)
		})
	}))

	//	@Summary		Call a function
	//	@Description	FCALL, or FCALL_RO with read_only.
	//	@Tags			functions
	//	@Produce		json
	//	@Param			body	FunctionCallRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/functions/call [post]
	s.api(mux, "POST /api/v1/functions/call", bodyRoute(s, func(_ *http.Request, b *FunctionCallRequest) ([]any, func(any) any, error) {
		if b.Function == "" {
			return nil, nil, badRequest("function is required")
		}

		name := "fcall"
		if b.ReadOnly {
			name = "fcall_ro"
		}

		return cmd(name, b.Function, len(b.Keys), b.Keys, b.Args), nil, nil
	}))
}

type EvalRequest struct {
	// Lua script, alternative to sha.
	Script string `json:"script"`
	// SHA1 of a loaded script.
	SHA string `json:"sha"`
	// Keys: they route the call in cluster.
	Keys []codec.Arg `json:"keys"`
	// Arguments.
	Args []codec.Arg `json:"args"`
	// Use the read-only variant of the command.
	ReadOnly bool `json:"read_only"`
}

type FunctionCallRequest struct {
	// Function name.
	Function string `json:"function"`
	// Keys.
	Keys []codec.Arg `json:"keys"`
	// Arguments.
	Args []codec.Arg `json:"args"`
	// Use the read-only variant of the command.
	ReadOnly bool `json:"read_only"`
}

func (s *Server) aclRoutes(mux *http.ServeMux) {
	fanOut := func(fn func(r *http.Request, sel service.NodeSelector) (any, error)) jsonHandler {
		return func(r *http.Request) (any, error) {
			return withSelector(r, func(sel service.NodeSelector) (any, error) { return fn(r, sel) })
		}
	}

	//	@Summary		Users
	//	@Description	ACL USERS.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/users [get]
	s.api(mux, "GET /api/v1/acl/users", fanOut(func(r *http.Request, sel service.NodeSelector) (any, error) {
		return s.svc.ACLUsers(r.Context(), sel)
	}))
	//	@Summary		Users with rules
	//	@Description	ACL LIST.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/list [get]
	s.api(mux, "GET /api/v1/acl/list", fanOut(func(r *http.Request, sel service.NodeSelector) (any, error) {
		return s.svc.ACLList(r.Context(), sel)
	}))
	//	@Summary		Get a user
	//	@Description	ACL GETUSER.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			user	path		string	true	"User"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/users/{user} [get]
	s.api(mux, "GET /api/v1/acl/users/{user}", fanOut(func(r *http.Request, sel service.NodeSelector) (any, error) {
		return s.svc.ACLGetUser(r.Context(), sel, r.PathValue("user"))
	}))

	//	@Summary		Create or change a user
	//	@Description	ACL SETUSER on every data node.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			user	path		string				true	"User"
	//	@Param			body	ACLSetUserRequest	true	"Request body"
	//	@Param			node	query		string				false	"Node address"
	//	@Param			target	query		string				false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/users/{user} [put]
	s.api(mux, "PUT /api/v1/acl/users/{user}", writeOp(func(r *http.Request) (any, error) {
		var body ACLSetUserRequest

		return withBody(r, &body, func(sel service.NodeSelector) (any, error) {
			return s.svc.ACLSetUser(r.Context(), sel, r.PathValue("user"), body.Rules, body.Reset)
		})
	}))
	//	@Summary		Delete a user
	//	@Description	ACL DELUSER on every data node.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			user	path		string	true	"User"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/users/{user} [delete]
	s.api(mux, "DELETE /api/v1/acl/users/{user}", writeOp(fanOut(func(r *http.Request, sel service.NodeSelector) (any, error) {
		return s.svc.ACLDelUser(r.Context(), sel, []string{r.PathValue("user")})
	})))

	//	@Summary		Security events
	//	@Description	ACL LOG.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			count	query		int		false	"Entries, default 10"
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/log [get]
	s.api(mux, "GET /api/v1/acl/log", fanOut(func(r *http.Request, sel service.NodeSelector) (any, error) {
		count, err := queryInt(r, "count", 10) //nolint:mnd // redis default
		if err != nil {
			return nil, err
		}

		return s.svc.ACLLog(r.Context(), sel, count)
	}))
	//	@Summary		Clear security events
	//	@Description	ACL LOG RESET.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
	//	@Success		200		{object}	Response{result=service.FanOutResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/log [delete]
	s.api(mux, "DELETE /api/v1/acl/log", writeOp(fanOut(func(r *http.Request, sel service.NodeSelector) (any, error) {
		return s.svc.ACLLogReset(r.Context(), sel)
	})))

	for _, cmd := range []string{"save", "load"} {
		//	@Summary		Save or load the ACL file
		//	@Description	ACL SAVE or ACL LOAD.
		//	@Tags			acl
		//	@Produce		json
		//	@Param			node	query		string	false	"Node address"
		//	@Param			target	query		string	false	"masters, replicas, all or sentinels"
		//	@Success		200		{object}	Response{result=service.FanOutResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/acl/save [post]
		//	@Router			/acl/load [post]
		s.api(mux, "POST /api/v1/acl/"+cmd, writeOp(fanOut(func(r *http.Request, sel service.NodeSelector) (any, error) {
			return s.svc.ACLPersist(r.Context(), sel, cmd)
		})))
	}

	//	@Summary		Check a permission
	//	@Description	ACL DRYRUN: "OK" or the reason of the denial.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			body	ACLDryRunRequest	true	"Request body"
	//	@Param			node	query		string				false	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/dryrun [post]
	s.api(mux, "POST /api/v1/acl/dryrun", func(r *http.Request) (any, error) {
		var body ACLDryRunRequest

		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		return s.svc.ACLDryRun(r.Context(), r.URL.Query().Get("node"), body.User, body.Command)
	})
	//	@Summary		Current user
	//	@Description	ACL WHOAMI.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			node	query		string	false	"Node address"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/whoami [get]
	s.api(mux, "GET /api/v1/acl/whoami", func(r *http.Request) (any, error) {
		return s.svc.ACLWhoAmI(r.Context(), r.URL.Query().Get("node"))
	})
	//	@Summary		ACL categories
	//	@Description	ACL CAT.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			category	query		string	false	"Category to list commands of"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/categories [get]
	s.api(mux, "GET /api/v1/acl/categories", func(r *http.Request) (any, error) {
		return s.svc.ACLCat(r.Context(), r.URL.Query().Get("category"))
	})
	//	@Summary		Generate a password
	//	@Description	ACL GENPASS.
	//	@Tags			acl
	//	@Produce		json
	//	@Param			bits	query		int	false	"Bits"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/acl/genpass [get]
	s.api(mux, "GET /api/v1/acl/genpass", func(r *http.Request) (any, error) {
		bits, err := queryInt(r, "bits", 0)
		if err != nil {
			return nil, err
		}

		return s.svc.ACLGenPass(r.Context(), bits)
	})
}

type ScriptLoadRequest struct {
	// Lua script.
	Script string `json:"script"`
}

type FunctionLoadRequest struct {
	// Library code starting with #!lua name=<library>.
	Code string `json:"code"`
	// Overwrite existing data.
	Replace bool `json:"replace"`
}

type FunctionRestoreRequest struct {
	// Dump from GET /functions/dump.
	Dump codec.Arg `json:"dump"`
	// Restore policy, append by default.
	Policy string `json:"policy" enums:"append,replace,flush"`
}

type ACLSetUserRequest struct {
	// ACL rules such as on, >password, ~keys:*, +@read.
	Rules []string `json:"rules"`
	// Reset the user before applying the rules.
	Reset bool `json:"reset"`
}

type ACLDryRunRequest struct {
	// User name.
	User string `json:"user"`
	// Command and arguments.
	Command []string `json:"command"`
}
