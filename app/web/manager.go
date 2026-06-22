package web

import (
	"context"
	"sync"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/spf13/viper"

	"github.com/iyear/tdl/core/storage"
	tclientcore "github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/kv"
	"github.com/iyear/tdl/pkg/tclient"
)

// ClientManager keeps one long-lived, authorized Telegram client per namespace
// (account). The gotd client.Run is blocking and only holds the connection for
// the lifetime of its callback, so each account runs in its own goroutine with a
// callback that stays alive until the server shuts down. HTTP requests dispatch
// operations onto the already-connected client.
type ClientManager struct {
	base   context.Context // server-lifetime context, carries the logger
	engine kv.Storage

	mu      sync.Mutex
	clients map[string]*liveClient
}

func NewClientManager(base context.Context, engine kv.Storage) *ClientManager {
	return &ClientManager{
		base:    base,
		engine:  engine,
		clients: make(map[string]*liveClient),
	}
}

// Get returns a ready (connected + authorized) client for ns, lazily creating
// and starting it on first use. It returns an error if the namespace is not
// logged in, in which case the entry is dropped so a later login can retry.
func (m *ClientManager) Get(ns string) (*liveClient, error) {
	m.mu.Lock()
	lc, ok := m.clients[ns]
	if !ok {
		lc = &liveClient{ns: ns, ready: make(chan struct{})}
		m.clients[ns] = lc
	}
	m.mu.Unlock()

	if err := lc.ensureStarted(m.base, m.engine); err != nil {
		m.mu.Lock()
		// only drop if it's still the same failed instance
		if cur, ok := m.clients[ns]; ok && cur == lc {
			delete(m.clients, ns)
		}
		m.mu.Unlock()
		return nil, err
	}
	return lc, nil
}

// Drop stops and removes the live client for ns (if any), releasing its hold on
// the namespace's storage so it can be deleted. It is safe to call when ns has
// no live client.
func (m *ClientManager) Drop(ns string) {
	m.mu.Lock()
	lc, ok := m.clients[ns]
	if ok {
		delete(m.clients, ns)
	}
	m.mu.Unlock()
	if ok {
		lc.stop()
	}
}

// Close stops every live client (cancels their run goroutines).
func (m *ClientManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, lc := range m.clients {
		lc.stop()
	}
	m.clients = make(map[string]*liveClient)
}

// DropAll stops every live client and clears the map so the next Get for each
// account reconnects from scratch. Used after a proxy change so all accounts
// re-dial through the new proxy. Unlike Close, the manager stays usable.
func (m *ClientManager) DropAll() {
	m.mu.Lock()
	old := m.clients
	m.clients = make(map[string]*liveClient)
	m.mu.Unlock()
	for _, lc := range old {
		lc.stop()
	}
}

// liveClient is a single account's persistent connection.
type liveClient struct {
	ns string

	startOnce sync.Once
	ready     chan struct{}
	readyErr  error

	client *telegram.Client
	kvd    storage.Storage

	runCtx context.Context
	cancel context.CancelFunc

	opMu sync.Mutex // serializes heavy operations on this account (anti-flood)
}

func (lc *liveClient) ensureStarted(base context.Context, engine kv.Storage) error {
	lc.startOnce.Do(func() {
		lc.runCtx, lc.cancel = context.WithCancel(base)

		kvd, err := engine.Open(lc.ns)
		if err != nil {
			lc.readyErr = errors.Wrap(err, "open kv")
			close(lc.ready)
			return
		}
		lc.kvd = kvd

		client, err := tclient.New(lc.runCtx, tclient.Options{
			KV:               kvd,
			Proxy:            viper.GetString(consts.FlagProxy),
			NTP:              viper.GetString(consts.FlagNTP),
			ReconnectTimeout: viper.GetDuration(consts.FlagReconnectTimeout),
			UpdateHandler:    nil,
		}, false)
		if err != nil {
			lc.cancel()
			lc.readyErr = errors.Wrap(err, "create client")
			close(lc.ready)
			return
		}
		lc.client = client

		runErrCh := make(chan error, 1)
		go func() {
			err := tclientcore.RunWithAuth(lc.runCtx, client, func(ctx context.Context) error {
				// connected & authorized: signal readiness, then keep the
				// connection alive until the manager cancels runCtx.
				select {
				case runErrCh <- nil:
				default:
				}
				<-ctx.Done()
				return ctx.Err()
			})
			// If the callback never ran (e.g. not authorized or connection
			// failure), surface the error to the waiter below.
			select {
			case runErrCh <- err:
			default:
			}
		}()

		if err := <-runErrCh; err != nil {
			lc.cancel()
			lc.readyErr = err
			close(lc.ready)
			return
		}

		close(lc.ready)
	})

	<-lc.ready
	return lc.readyErr
}

func (lc *liveClient) stop() {
	if lc.cancel != nil {
		lc.cancel()
	}
}

// do runs fn serialized on this account, with a context derived from the live
// connection that is also canceled if reqCtx (the HTTP request) is canceled.
// Use for short, request-scoped operations.
func (lc *liveClient) do(reqCtx context.Context, fn func(ctx context.Context, c *telegram.Client, kvd storage.Storage) error) error {
	lc.opMu.Lock()
	defer lc.opMu.Unlock()

	opCtx, cancel := context.WithCancel(lc.runCtx)
	defer cancel()
	go func() {
		select {
		case <-reqCtx.Done():
			cancel()
		case <-opCtx.Done():
		}
	}()

	return fn(opCtx, lc.client, lc.kvd)
}

// backgroundCtx runs fn serialized on this account, under the given context
// (derived from the live connection, with a per-task cancel layered on by the
// caller). Use for long-running tasks like uploads. If ctx is already canceled
// by the time the op slot is acquired (e.g. a queued task was canceled while
// waiting), fn is skipped.
func (lc *liveClient) backgroundCtx(ctx context.Context, fn func(ctx context.Context, c *telegram.Client, kvd storage.Storage) error) error {
	lc.opMu.Lock()
	defer lc.opMu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return fn(ctx, lc.client, lc.kvd)
}
