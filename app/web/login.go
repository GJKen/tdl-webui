package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/skip2/go-qrcode"
	"github.com/spf13/viper"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/key"
	"github.com/iyear/tdl/pkg/kv"
	"github.com/iyear/tdl/pkg/tclient"
)

// loginTimeout bounds how long an interactive login may stay pending before its
// goroutine and client are torn down.
const loginTimeout = 5 * time.Minute

type LoginStage string

const (
	StageStarting     LoginStage = "starting"
	StageNeedQR       LoginStage = "need_qr"
	StageNeedCode     LoginStage = "need_code"
	StageNeedPassword LoginStage = "need_password"
	StageDone         LoginStage = "done"
	StageError        LoginStage = "error"
)

type SelfInfo struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type LoginStatus struct {
	Stage LoginStage `json:"stage"`
	QR    string     `json:"qr,omitempty"` // QR login: PNG data URI to render as an image
	Error string     `json:"error,omitempty"`
	Self  *SelfInfo  `json:"self,omitempty"`
}

// loginSession holds the state of one in-progress code login. The gotd auth flow
// runs synchronously inside client.Run and blocks on the webAuth callbacks; the
// HTTP handlers feed those callbacks through codeCh / pwdCh.
type loginSession struct {
	id    string
	ns    string
	phone string

	mu    sync.Mutex
	stage LoginStage
	errs  string
	self  *SelfInfo
	qr    string // QR login: current PNG data URI (refreshed as the token rotates)

	codeCh chan string
	pwdCh  chan string

	cancel    context.CancelFunc
	nsCreated bool // QR login created this namespace this round; clean it up if login never finishes
}

func (s *loginSession) setStage(st LoginStage) {
	s.mu.Lock()
	s.stage = st
	s.mu.Unlock()
}

// setQR stores the latest QR image and marks the session as awaiting a scan.
// qrlogin.Auth calls the show callback again whenever the token rotates (~30s),
// so this is invoked repeatedly; the frontend just polls for the newest image.
func (s *loginSession) setQR(uri string) {
	s.mu.Lock()
	s.qr = uri
	s.stage = StageNeedQR
	s.mu.Unlock()
}

func (s *loginSession) snapshot() LoginStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return LoginStatus{Stage: s.stage, QR: s.qr, Error: s.errs, Self: s.self}
}

// webAuth implements gotd's auth.UserAuthenticator, bridging the blocking auth
// flow to asynchronous HTTP input.
type webAuth struct {
	session *loginSession
}

func (a webAuth) Phone(_ context.Context) (string, error) {
	return strings.TrimSpace(a.session.phone), nil
}

func (a webAuth) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	a.session.setStage(StageNeedCode)
	select {
	case code := <-a.session.codeCh:
		return strings.TrimSpace(code), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (a webAuth) Password(ctx context.Context) (string, error) {
	a.session.setStage(StageNeedPassword)
	select {
	case pwd := <-a.session.pwdCh:
		return strings.TrimSpace(pwd), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (a webAuth) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("don't support sign up Telegram account")
}

func (a webAuth) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return &auth.SignUpRequired{TermsOfService: tos}
}

// LoginManager drives code-based logins for the web UI.
type LoginManager struct {
	base   context.Context
	engine kv.Storage

	mu       sync.Mutex
	sessions map[string]*loginSession
}

func NewLoginManager(base context.Context, engine kv.Storage) *LoginManager {
	return &LoginManager{
		base:     base,
		engine:   engine,
		sessions: make(map[string]*loginSession),
	}
}

// Start kicks off a code login for the given namespace and phone. A new
// namespace is created on demand. It returns a login id used to drive the flow.
func (m *LoginManager) Start(ns, phone string) (string, error) {
	phone = strings.TrimSpace(phone)
	ns, err := sanitizeNamespace(ns)
	if err != nil {
		return "", err
	}
	if phone == "" {
		return "", errors.New("phone is required")
	}

	kvd, err := m.engine.Open(ns)
	if err != nil {
		return "", errors.Wrap(err, "open kv")
	}

	id, err := randomID()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(m.base, loginTimeout)
	s := &loginSession{
		id:     id,
		ns:     ns,
		phone:  phone,
		stage:  StageStarting,
		codeCh: make(chan string, 1),
		pwdCh:  make(chan string, 1),
		cancel: cancel,
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	go m.run(ctx, s, kvd)
	return id, nil
}

func (m *LoginManager) run(ctx context.Context, s *loginSession, kvd storage.Storage) {
	defer s.cancel()

	fail := func(err error) {
		s.mu.Lock()
		s.stage = StageError
		s.errs = err.Error()
		s.mu.Unlock()
	}

	// mark app type as desktop, identical to `tdl login -T code`.
	if err := kvd.Set(ctx, key.App(), []byte(tclient.AppDesktop)); err != nil {
		fail(errors.Wrap(err, "set app"))
		return
	}

	c, err := tclient.New(ctx, tclient.Options{
		KV:               kvd,
		Proxy:            viper.GetString(consts.FlagProxy),
		NTP:              viper.GetString(consts.FlagNTP),
		ReconnectTimeout: viper.GetDuration(consts.FlagReconnectTimeout),
		UpdateHandler:    nil,
	}, true)
	if err != nil {
		fail(errors.Wrap(err, "create client"))
		return
	}

	err = c.Run(ctx, func(ctx context.Context) error {
		if err := c.Ping(ctx); err != nil {
			return errors.Wrap(err, "ping")
		}

		flow := auth.NewFlow(webAuth{session: s}, auth.SendCodeOptions{})
		if err := c.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}

		user, err := c.Self(ctx)
		if err != nil {
			return errors.Wrap(err, "get self")
		}

		self := &SelfInfo{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName}
		cacheSelf(ctx, kvd, self)

		s.mu.Lock()
		s.stage = StageDone
		s.self = self
		s.mu.Unlock()
		return nil
	})
	if err != nil {
		fail(err)
	}
}

// StartQR kicks off a QR-code login for the given namespace. Unlike code login
// it needs no phone: the user scans the rendered QR with their Telegram app.
func (m *LoginManager) StartQR(ns string) (string, error) {
	ns, err := sanitizeNamespace(ns)
	if err != nil {
		return "", err
	}

	// Remember whether this namespace already existed. gotd writes an auth-key
	// session as soon as it connects (before the user scans), so a QR login that
	// is abandoned would leave a session-only namespace that shows up as a ghost
	// account. If we created it this round, runQR cleans it up unless login
	// actually completes. An existing namespace is never touched.
	created := true
	if names, err := m.engine.Namespaces(); err == nil {
		for _, n := range names {
			if n == ns {
				created = false
				break
			}
		}
	}

	kvd, err := m.engine.Open(ns)
	if err != nil {
		return "", errors.Wrap(err, "open kv")
	}

	id, err := randomID()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(m.base, loginTimeout)
	s := &loginSession{
		id:        id,
		ns:        ns,
		stage:     StageStarting,
		codeCh:    make(chan string, 1),
		pwdCh:     make(chan string, 1),
		cancel:    cancel,
		nsCreated: created,
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	go m.runQR(ctx, s, kvd)
	return id, nil
}

// runQR mirrors run() but drives gotd's QR login flow instead of the code flow.
// It needs an UpdateDispatcher so qrlogin can observe UpdateLoginToken (the
// "scanned & accepted" signal); the show callback fires again on every token
// rotation (~30s), refreshing the image. 2FA is handled inline, same as
// `tdl login -T qr`.
func (m *LoginManager) runQR(ctx context.Context, s *loginSession, kvd storage.Storage) {
	defer s.cancel()

	fail := func(err error) {
		s.mu.Lock()
		s.stage = StageError
		s.errs = err.Error()
		s.mu.Unlock()
	}

	// mark app type as desktop, identical to `tdl login -T qr`.
	if err := kvd.Set(ctx, key.App(), []byte(tclient.AppDesktop)); err != nil {
		fail(errors.Wrap(err, "set app"))
		return
	}

	d := tg.NewUpdateDispatcher()
	c, err := tclient.New(ctx, tclient.Options{
		KV:               kvd,
		Proxy:            viper.GetString(consts.FlagProxy),
		NTP:              viper.GetString(consts.FlagNTP),
		ReconnectTimeout: viper.GetDuration(consts.FlagReconnectTimeout),
		UpdateHandler:    d,
	}, true)
	if err != nil {
		fail(errors.Wrap(err, "create client"))
		return
	}

	err = c.Run(ctx, func(ctx context.Context) error {
		_, err := c.QR().Auth(ctx, qrlogin.OnLoginToken(d), func(ctx context.Context, token qrlogin.Token) error {
			png, err := qrcode.Encode(token.URL(), qrcode.Medium, 256)
			if err != nil {
				return errors.Wrap(err, "encode qr")
			}
			s.setQR("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
			return nil
		})
		if err != nil {
			// https://core.telegram.org/api/auth#2fa
			if !tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
				return err
			}
			s.setStage(StageNeedPassword)
			select {
			case pwd := <-s.pwdCh:
				if _, err := c.Auth().Password(ctx, strings.TrimSpace(pwd)); err != nil {
					return errors.Wrap(err, "2fa auth")
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		user, err := c.Self(ctx)
		if err != nil {
			return errors.Wrap(err, "get self")
		}

		self := &SelfInfo{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName}
		cacheSelf(ctx, kvd, self)

		s.mu.Lock()
		s.stage = StageDone
		s.self = self
		s.mu.Unlock()
		return nil
	})
	if err != nil {
		fail(err)
	}

	// Clean up a ghost namespace: if we created this ns this round but the login
	// never completed (cancelled / timed out / errored), the only thing written
	// is gotd's connection auth key, which would otherwise linger as an empty
	// account. The client is already torn down (c.Run has returned), so the
	// storage lock is released and DeleteNamespace can proceed.
	s.mu.Lock()
	done := s.stage == StageDone
	s.mu.Unlock()
	if !done && s.nsCreated {
		_ = m.engine.DeleteNamespace(s.ns)
	}
}

// Cancel aborts an in-progress login. For a QR login that never completed this
// cancels its context, which unblocks runQR and triggers ghost-namespace
// cleanup. No-op if the id is unknown or already finished.
func (m *LoginManager) Cancel(id string) {
	if s, ok := m.get(id); ok {
		s.cancel()
	}
}

func (m *LoginManager) get(id string) (*loginSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *LoginManager) SubmitCode(id, code string) error {
	s, ok := m.get(id)
	if !ok {
		return errors.New("login session not found")
	}
	select {
	case s.codeCh <- code:
		return nil
	default:
		return errors.New("not waiting for a code right now")
	}
}

func (m *LoginManager) SubmitPassword(id, pwd string) error {
	s, ok := m.get(id)
	if !ok {
		return errors.New("login session not found")
	}
	select {
	case s.pwdCh <- pwd:
		return nil
	default:
		return errors.New("not waiting for a password right now")
	}
}

func (m *LoginManager) Status(id string) (LoginStatus, bool) {
	s, ok := m.get(id)
	if !ok {
		return LoginStatus{}, false
	}
	return s.snapshot(), true
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", errors.Wrap(err, "generate id")
	}
	return hex.EncodeToString(b), nil
}

// cacheSelf best-effort stores the account identity in its namespace so the
// account list can render id/name without reconnecting. Failures are ignored.
func cacheSelf(ctx context.Context, kvd storage.Storage, self *SelfInfo) {
	b, err := json.Marshal(self)
	if err != nil {
		return
	}
	_ = kvd.Set(ctx, key.WebSelf(), b)
}
