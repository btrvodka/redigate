// Package webui serves a small htmx-based web interface on top of the service
// layer: an overview of the deployment, a key browser and a command console.
// The HTTP API stays the primary interface; the UI is optional (UI_ENABLED).
package webui

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/btrvodka/redigate/internal/auth"
	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/service"
)

const (
	cookieName = "redigate_token"
	cookieTTL  = 7 * 24 * time.Hour
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type contextKey struct{}

// UI is an http.Handler serving the interface under /ui/.
type UI struct {
	cfg     *config.Config
	svc     *service.Service
	log     *slog.Logger
	version string
	pages   map[string]*template.Template
	mux     *http.ServeMux
}

func New(cfg *config.Config, svc *service.Service, logger *slog.Logger, version string) (*UI, error) {
	ui := &UI{cfg: cfg, svc: svc, log: logger, version: version, pages: make(map[string]*template.Template)}

	for _, page := range []string{"login", "overview", "keys", "console"} {
		tmpl, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+page+".html")
		if err != nil {
			return nil, err //nolint:wrapcheck // embedded templates
		}

		ui.pages[page] = tmpl
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err //nolint:wrapcheck // embedded files
	}

	mux := http.NewServeMux()
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", cacheFor(time.Hour, http.FileServerFS(static))))
	mux.HandleFunc("GET /ui/login", ui.loginPage)
	mux.HandleFunc("POST /ui/login", ui.login)
	mux.HandleFunc("POST /ui/logout", ui.logout)
	mux.Handle("GET /ui/{$}", ui.protected(ui.overview))
	mux.Handle("GET /ui/keys", ui.protected(ui.keysPage))
	mux.Handle("GET /ui/keys/rows", ui.protected(ui.keyRows))
	mux.Handle("GET /ui/key", ui.protected(ui.keyView))
	mux.Handle("DELETE /ui/key", ui.protected(ui.deleteKey))
	mux.Handle("PUT /ui/key/ttl", ui.protected(ui.setTTL))
	mux.Handle("GET /ui/console", ui.protected(ui.consolePage))
	mux.Handle("POST /ui/console", ui.protected(ui.runCommand))
	ui.mux = mux

	return ui, nil
}

func (ui *UI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	// The UI has destructive buttons: it must not be framed by other sites.
	header.Set("X-Frame-Options", "DENY")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "same-origin")

	ui.mux.ServeHTTP(w, r)
}

func cacheFor(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(d.Seconds())))
		next.ServeHTTP(w, r)
	})
}

// protected resolves the access level from the session cookie. Requests that
// change data must come from htmx (HX-Request header): together with the
// SameSite=Strict cookie this protects against CSRF.
func (ui *UI) protected(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var token string
		if cookie, err := r.Cookie(cookieName); err == nil {
			token = cookie.Value
		}

		access, err := auth.Resolve(ui.cfg.Auth, token)
		if err != nil {
			if isHTMX(r) {
				w.Header().Set("HX-Redirect", "/ui/login")
				w.WriteHeader(http.StatusUnauthorized)

				return
			}

			http.Redirect(w, r, "/ui/login", http.StatusSeeOther)

			return
		}

		if r.Method != http.MethodGet && !isHTMX(r) {
			http.Error(w, "requests changing data must be sent by the UI", http.StatusForbidden)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), ui.cfg.Limits.RequestTimeout)
		defer cancel()

		next(w, r.WithContext(context.WithValue(ctx, contextKey{}, access)))
	})
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func accessOf(r *http.Request) auth.Access {
	access, _ := r.Context().Value(contextKey{}).(auth.Access)

	return access
}

func (ui *UI) loginPage(w http.ResponseWriter, r *http.Request) {
	if !ui.cfg.Auth.Enabled() {
		http.Redirect(w, r, "/ui/", http.StatusSeeOther)

		return
	}

	ui.render(w, r, http.StatusOK, "login", "page", pageData{Title: "Sign in"})
}

func (ui *UI) login(w http.ResponseWriter, r *http.Request) {
	if !ui.cfg.Auth.Enabled() {
		http.Redirect(w, r, "/ui/", http.StatusSeeOther)

		return
	}

	token := r.PostFormValue("token")

	if _, err := auth.Resolve(ui.cfg.Auth, token); err != nil {
		ui.render(w, r, http.StatusUnauthorized, "login", "page", pageData{Title: "Sign in", Error: "Invalid token"})

		return
	}

	// Secure only over TLS: redigate usually runs on plain http://localhost in development.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // see above
		Name:     cookieName,
		Value:    token,
		Path:     "/ui/",
		MaxAge:   int(cookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/ui/", http.StatusSeeOther)
}

func (ui *UI) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // clears the cookie
		Name: cookieName, Path: "/ui/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}

// pageData is shared by all pages; Data holds the page specific content.
type pageData struct {
	Title     string
	Nav       string
	Version   string
	Topology  string
	ReadOnly  bool
	AuthOn    bool
	Error     string
	Data      any
	RequestID string
}

func (ui *UI) page(r *http.Request, title, nav string, data any) pageData {
	return pageData{
		Title:    title,
		Nav:      nav,
		Version:  ui.version,
		Topology: ui.svc.TopologyName(),
		ReadOnly: accessOf(r) < auth.Full,
		AuthOn:   ui.cfg.Auth.Enabled(),
		Data:     data,
	}
}

// render executes a named template of a page; "page" renders the whole page.
func (ui *UI) render(w http.ResponseWriter, _ *http.Request, status int, page, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	if err := ui.pages[page].ExecuteTemplate(w, name, data); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		ui.log.Warn("render ui template", slog.String("template", name), slog.Any("error", err))
	}
}

// fail renders the error fragment of the layout; htmx 4 swaps error responses too.
func (ui *UI) fail(w http.ResponseWriter, r *http.Request, err error) {
	ui.render(w, r, statusOf(err), "keys", "error", err.Error())
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, service.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, service.ErrInvalidRequest), errors.Is(err, service.ErrCrossSlot), errors.Is(err, service.ErrUnsupportedCommand):
		return http.StatusBadRequest
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}
