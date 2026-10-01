package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/service"
)

type Metrics interface {
	ObserveHTTPRequest(route string, code int, duration time.Duration)
}

type Server struct {
	cfg     *config.Config
	svc     *service.Service
	log     *slog.Logger
	metrics Metrics
	version string
	// streams limits concurrent server-sent event streams.
	streams chan struct{}
	// patterns lists registered API routes, see Routes.
	patterns []string

	handler http.Handler
}

// Option configures the server.
type Option func(*options)

type options struct {
	ui        http.Handler
	swaggerUI bool
}

// WithUI serves the web UI at /ui/ and redirects / to it.
func WithUI(ui http.Handler) Option {
	return func(o *options) { o.ui = ui }
}

// WithSwaggerUI serves Swagger UI for the API at /api/v1/docs/.
func WithSwaggerUI() Option {
	return func(o *options) { o.swaggerUI = true }
}

func New(cfg *config.Config, svc *service.Service, logger *slog.Logger, metrics Metrics, version string, opts ...Option) *Server {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	s := &Server{
		cfg:     cfg,
		svc:     svc,
		log:     logger,
		metrics: metrics,
		version: version,
		streams: make(chan struct{}, cfg.Limits.MaxStreams),
	}

	mux := http.NewServeMux()
	s.routes(mux)

	if o.swaggerUI {
		swaggerUIRoutes(mux)
	}

	if o.ui != nil {
		mux.Handle("/ui/", o.ui)
		mux.Handle("GET /{$}", http.RedirectHandler("/ui/", http.StatusFound))
	}

	s.handler = withRequestID(s.withAccessLog(s.withRecover(mux)))

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	s.openapiRoutes(mux)

	//	@Summary		Ping every node
	//	@Description	Pings all nodes concurrently and reports latency per node.
	//	@Tags			general
	//	@Produce		json
	//	@Success		200		{object}	Response{result=service.PingResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/ping [get]
	s.api(mux, "GET /api/v1/ping", s.handlePing)
	//	@Summary		Topology and nodes
	//	@Description	The detected topology (standalone, cluster or sentinel) and known nodes.
	//	@Tags			general
	//	@Produce		json
	//	@Success		200		{object}	Response{result=service.TopologyInfo}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/topology [get]
	s.api(mux, "GET /api/v1/topology", s.handleTopology)
	//	@Summary		Run a command
	//	@Description	Runs any command. The body is either JSON (CommandRequest) or a redis-cli style command line such as `SET key "a value"`.
	//	@Description	Commands changing connection state (SELECT, AUTH, HELLO, MULTI, WATCH, CLIENT REPLY, ...) run on a dedicated connection.
	//	@Description	SUBSCRIBE, MONITOR and SYNC are served by streaming endpoints. With a fan-out target (masters, replicas, all, sentinels) the result is service.FanOutResult.
	//	@Tags			general
	//	@Accept			json,plain
	//	@Produce		json
	//	@Param			body		body		CommandRequest	true	"JSON or a command line"
	//	@Param			node		query		string			false	"Node address"
	//	@Param			target		query		string			false	"masters, replicas, all or sentinels"
	//	@Param			db			query		int				false	"Database"
	//	@Param			encoding	query		string			false	"auto, utf8 or base64"
	//	@Param			timeout		query		string			false	"Timeout for blocking commands, e.g. 30s"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/command [post]
	s.api(mux, "POST /api/v1/command", s.handleCommand)
	//	@Summary		Run commands in one round trip
	//	@Description	The body is JSON (PipelineRequest) or one redis-cli style command per line, lines starting with # are skipped.
	//	@Description	atomic wraps the commands into MULTI/EXEC; session runs them on a dedicated connection, which allows SELECT, AUTH, WATCH and manual MULTI/EXEC.
	//	@Description	In cluster atomic pipelines and sessions require keys of a single slot.
	//	@Tags			general
	//	@Accept			json,plain
	//	@Produce		json
	//	@Param			body		body		PipelineRequest	true	"JSON or command lines"
	//	@Param			node		query		string			false	"Node address"
	//	@Param			db			query		int				false	"Database"
	//	@Param			atomic		query		bool			false	"MULTI/EXEC"
	//	@Param			session		query		bool			false	"Dedicated connection"
	//	@Param			encoding	query		string			false	"auto, utf8 or base64"
	//	@Success		200			{object}	Response{result=service.PipelineResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/pipeline [post]
	s.api(mux, "POST /api/v1/pipeline", s.handlePipeline)
	s.serverRoutes(mux)
	s.clusterRoutes(mux)
	s.keyRoutes(mux)
	s.stringRoutes(mux)
	s.hashRoutes(mux)
	s.listRoutes(mux)
	s.setRoutes(mux)
	s.zsetRoutes(mux)
	s.streamRoutes(mux)
	s.bitmapRoutes(mux)
	s.geoRoutes(mux)
	s.pubsubRoutes(mux)
	s.scriptRoutes(mux)
	s.aclRoutes(mux)
	s.transferRoutes(mux)
	s.jsonRoutes(mux)
	s.searchRoutes(mux)
	s.timeseriesRoutes(mux)
	s.bloomRoutes(mux)
	s.sketchRoutes(mux)
	s.vectorsetRoutes(mux)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, newAPIError(http.StatusNotFound, CodeRouteNotFound, "route %s %s not found", r.Method, r.URL.Path))
	})
}

// Routes returns the patterns of all API routes ("GET /api/v1/ping", ...).
func (s *Server) Routes() []string {
	return slices.Clone(s.patterns)
}

// handle registers an API route and remembers its pattern.
func (s *Server) handle(mux *http.ServeMux, pattern string, h http.Handler) {
	s.patterns = append(s.patterns, pattern)
	mux.Handle(pattern, h)
}

// api registers an authenticated endpoint returning JSON.
func (s *Server) api(mux *http.ServeMux, pattern string, h jsonHandler) {
	s.handle(mux, pattern, s.protected(func(w http.ResponseWriter, r *http.Request) {
		timeout, err := s.requestTimeout(r)
		if err != nil {
			s.writeError(w, r, err)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		r = r.WithContext(ctx)

		result, err := h(r)
		if err != nil {
			s.writeError(w, r, err)

			return
		}

		s.writeResult(w, r, result)
	}))
}

// requestTimeout returns REQUEST_TIMEOUT or the "timeout" query parameter,
// which is useful for blocking commands and is limited by MAX_BLOCK_TIMEOUT.
func (s *Server) requestTimeout(r *http.Request) (time.Duration, error) {
	limits := s.cfg.Limits
	if !r.URL.Query().Has("timeout") {
		return limits.RequestTimeout, nil
	}

	timeout, err := time.ParseDuration(r.URL.Query().Get("timeout"))
	if err != nil || timeout <= 0 {
		return 0, badRequest("invalid timeout %q, expected a duration like 5s", r.URL.Query().Get("timeout"))
	}

	if maxTimeout := max(limits.RequestTimeout, limits.MaxBlockTimeout); timeout > maxTimeout {
		return 0, badRequest("timeout %s exceeds the limit of %s", timeout, maxTimeout)
	}

	return timeout, nil
}

// protected authenticates the request and limits its body size to MAX_BODY_BYTES.
func (s *Server) protected(next http.HandlerFunc) http.Handler {
	return s.protectedLimit(next, s.cfg.Limits.MaxBodyBytes)
}

func (s *Server) protectedLimit(next http.HandlerFunc, bodyLimit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		access, err := s.authenticate(r)
		if err != nil {
			s.writeError(w, r, err)

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		next(w, r.WithContext(context.WithValue(r.Context(), accessKey, access)))
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Limits.RequestTimeout)
	defer cancel()

	if err := s.svc.Ready(ctx); err != nil {
		s.writeError(w, r, &APIError{
			Status:  http.StatusServiceUnavailable,
			Code:    CodeRedisUnavailable,
			Message: err.Error(),
			Err:     err,
		})

		return
	}

	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handlePing(r *http.Request) (any, error) {
	return s.svc.Ping(r.Context())
}

func (s *Server) handleTopology(r *http.Request) (any, error) {
	return s.svc.Topology(r.Context())
}
