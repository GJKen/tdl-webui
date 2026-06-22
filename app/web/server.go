package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gotd/td/telegram"
	"go.uber.org/zap"
	"golang.org/x/net/proxy"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/util/netutil"
	"github.com/iyear/tdl/pkg/key"
	"github.com/iyear/tdl/pkg/kv"
)

type Options struct {
	Host string
	Port int
}

// Server is the local web UI: a thin HTTP layer over the existing tdl business
// logic (uploads via app/up, chats via app/chat), with multi-account support
// backed by kv namespaces.
type Server struct {
	engine   kv.Storage
	clients  *ClientManager
	logins   *LoginManager
	tasks    *TaskStore
	settings *Settings
}

// Run starts the web server and blocks until ctx is canceled. ctx is expected to
// carry the logger and is used as the lifetime for all live clients.
func Run(ctx context.Context, engine kv.Storage, opts Options) error {
	clients := NewClientManager(ctx, engine)
	s := &Server{
		engine:  engine,
		clients: clients,
		logins:  NewLoginManager(ctx, engine),
		tasks:   NewTaskStore(),
		// load persisted rate/proxy and apply the proxy to viper BEFORE any client
		// is started, so the long-lived connections dial through the saved proxy.
		settings: loadSettings(ctx, engine, clients),
	}
	defer s.clients.Close()

	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	srv := &http.Server{
		Addr:    addr,
		Handler: s.router(),
	}

	errCh := make(chan error, 1)
	go func() {
		logctx.From(ctx).Info("web server listening", zap.String("addr", addr))
		fmt.Printf("\ntdl web UI is running at: http://%s\n(local only — do not expose this port)\n\n", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) router() http.Handler {
	r := mux.NewRouter()

	api := r.PathPrefix("/api").Subrouter()
	api.HandleFunc("/accounts", s.handleAccounts).Methods(http.MethodGet)
	api.HandleFunc("/accounts/{ns}", s.handleAccountDelete).Methods(http.MethodDelete)
	api.HandleFunc("/accounts/{ns}/alias", s.handleAliasSet).Methods(http.MethodPatch)
	api.HandleFunc("/accounts/{ns}/self", s.handleSelf).Methods(http.MethodGet)
	api.HandleFunc("/accounts/{ns}/login/start", s.handleLoginStart).Methods(http.MethodPost)
	api.HandleFunc("/login/{id}/status", s.handleLoginStatus).Methods(http.MethodGet)
	api.HandleFunc("/login/{id}/code", s.handleLoginCode).Methods(http.MethodPost)
	api.HandleFunc("/login/{id}/password", s.handleLoginPassword).Methods(http.MethodPost)
	api.HandleFunc("/accounts/{ns}/chats", s.handleChats).Methods(http.MethodGet)
	api.HandleFunc("/accounts/{ns}/upload", s.handleUpload).Methods(http.MethodPost)
	api.HandleFunc("/accounts/{ns}/upload-files", s.handleUploadFiles).Methods(http.MethodPost)
	api.HandleFunc("/accounts/{ns}/upload-album", s.handleUploadAlbum).Methods(http.MethodPost)
	api.HandleFunc("/tasks", s.handleTasks).Methods(http.MethodGet)
	api.HandleFunc("/tasks/{id}", s.handleTaskCancel).Methods(http.MethodDelete)
	api.HandleFunc("/settings", s.handleSettingsGet).Methods(http.MethodGet)
	api.HandleFunc("/settings", s.handleSettingsSet).Methods(http.MethodPut)
	api.HandleFunc("/settings/proxy-test", s.handleProxyTest).Methods(http.MethodPost)

	// static UI. no-cache so browsers always pick up the latest embedded assets
	// (this is a local dev tool; correctness beats caching here).
	r.PathPrefix("/").Handler(noCacheStatic(http.FileServer(http.FS(uiFS()))))

	return recoverMiddleware(r)
}

func noCacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		next.ServeHTTP(w, r)
	})
}

// --- account handlers ---

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	ns, err := s.engine.Namespaces()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	type account struct {
		Namespace string    `json:"namespace"`
		Alias     string    `json:"alias,omitempty"`
		Self      *SelfInfo `json:"self,omitempty"`
	}
	out := make([]account, 0, len(ns))
	for _, n := range ns {
		// Only list namespaces that are actually logged in (have a stored
		// session). This hides empty placeholders — e.g. a "default" namespace
		// that gets (re)created with no session after an account is deleted.
		kvd, err := s.engine.Open(n)
		if err != nil {
			continue
		}
		if v, err := kvd.Get(r.Context(), key.Session()); err != nil || len(v) == 0 {
			continue
		}

		acc := account{Namespace: n}
		// Read the locally-cached alias and identity without connecting to
		// Telegram; both are optional (a CLI-created account has neither yet).
		if v, err := kvd.Get(r.Context(), key.WebAlias()); err == nil && len(v) > 0 {
			acc.Alias = string(v)
		}
		if v, err := kvd.Get(r.Context(), key.WebSelf()); err == nil && len(v) > 0 {
			var self SelfInfo
			if json.Unmarshal(v, &self) == nil {
				acc.Self = &self
			}
		}
		out = append(out, acc)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSelf(w http.ResponseWriter, r *http.Request) {
	ns := mux.Vars(r)["ns"]

	lc, err := s.clients.Get(ns)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}

	var self SelfInfo
	err = lc.do(r.Context(), func(ctx context.Context, c *telegram.Client, kvd storage.Storage) error {
		u, err := c.Self(ctx)
		if err != nil {
			return err
		}
		self = SelfInfo{ID: u.ID, Username: u.Username, FirstName: u.FirstName, LastName: u.LastName}
		cacheSelf(ctx, kvd, &self)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, self)
}

// handleAliasSet sets (or clears, when empty) the web UI display name for an
// account. It only touches the namespace's KV, never the namespace itself, so
// the CLI keeps resolving the original namespace.
func (s *Server) handleAliasSet(w http.ResponseWriter, r *http.Request) {
	ns, err := sanitizeNamespace(mux.Vars(r)["ns"])
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	var body struct {
		Alias string `json:"alias"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	kvd, err := s.engine.Open(ns)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	alias := strings.TrimSpace(body.Alias)
	if alias == "" {
		if err := kvd.Delete(r.Context(), key.WebAlias()); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	} else if err := kvd.Set(r.Context(), key.WebAlias(), []byte(alias)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"alias": alias})
}

// handleAccountDelete drops the live client (releasing its hold on the storage)
// and then deletes the namespace and all of its data.
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	ns, err := sanitizeNamespace(mux.Vars(r)["ns"])
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	s.clients.Drop(ns)
	if err := s.engine.DeleteNamespace(ns); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- login handlers ---

func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	ns := mux.Vars(r)["ns"]

	var body struct {
		Phone string `json:"phone"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	id, err := s.logins.Start(ns, body.Phone)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"login_id": id})
}

func (s *Server) handleLoginStatus(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	st, ok := s.logins.Status(id)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("login session not found"))
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.logins.SubmitCode(id, body.Code); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLoginPassword(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.logins.SubmitPassword(id, body.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- task handlers ---

func (s *Server) handleTasks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tasks.List())
}

// handleTaskCancel aborts a task's upload and removes it (its bubble disappears).
func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	s.tasks.Cancel(mux.Vars(r)["id"])
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- settings handlers ---

// settingsBody is the JSON shape for both GET (response) and PUT (request) of
// the server-global web settings.
type settingsBody struct {
	Rate  string `json:"rate"`  // upload rate limit, human form ("5M"); "" = unlimited
	Proxy string `json:"proxy"` // proxy URL; "" = direct
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, _ *http.Request) {
	rateStr, proxy := s.settings.snapshot()
	writeJSON(w, http.StatusOK, settingsBody{Rate: rateStr, Proxy: proxy})
}

// handleSettingsSet applies a new upload rate and/or proxy. The rate retunes the
// shared limiter live (in-flight uploads included); changing the proxy
// reconnects all accounts.
func (s *Server) handleSettingsSet(w http.ResponseWriter, r *http.Request) {
	var body settingsBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if err := s.settings.setRate(r.Context(), body.Rate); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid rate: %w", err))
		return
	}
	proxyChanged, err := s.settings.setProxy(r.Context(), body.Proxy)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	rateStr, proxy := s.settings.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"rate":          rateStr,
		"proxy":         proxy,
		"proxy_changed": proxyChanged, // UI hint: accounts were reconnected
	})
}

// telegramTestDCs are a few Telegram production DC endpoints used only to check
// whether a proxy (or direct connection) can reach Telegram. A successful TCP
// connect to any one means "reachable".
var telegramTestDCs = []string{
	"149.154.167.51:443", // DC2 (Pluto)
	"149.154.175.50:443", // DC1 (Pluto)
	"91.108.56.130:443",  // DC5 (Multi)
}

// handleProxyTest tries to reach Telegram through the supplied proxy (empty =
// direct), without saving it. It reports {ok, error} so the UI can show a clean
// result either way.
func (s *Server) handleProxyTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Proxy string `json:"proxy"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	var dialer proxy.ContextDialer = proxy.Direct
	if p := strings.TrimSpace(body.Proxy); p != "" {
		d, err := netutil.NewProxy(p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("代理地址无效：%w", err))
			return
		}
		dialer = d
	}

	var lastErr error
	for _, addr := range telegramTestDCs {
		dctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		conn, err := dialer.DialContext(dctx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		lastErr = err
		if r.Context().Err() != nil { // client gave up
			break
		}
	}
	msg := "无法连接到 Telegram"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": msg})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("empty request body")
	}
	return json.NewDecoder(r.Body).Decode(v)
}

// sanitizeNamespace validates a namespace before it is used as a storage key /
// bolt filename, rejecting path traversal and characters illegal in filenames.
func sanitizeNamespace(ns string) (string, error) {
	ns = strings.TrimSpace(ns)
	if ns == "" {
		return "", errors.New("namespace is required")
	}
	if ns == "." || ns == ".." || strings.ContainsAny(ns, `/\:*?"<>|`) {
		return "", errors.New(`namespace must not contain / \ : * ? " < > |`)
	}
	if ns == settingsNS {
		return "", errors.New("namespace name is reserved")
	}
	return ns, nil
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeErr(w, http.StatusInternalServerError, fmt.Errorf("panic: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
