package web

import (
	"context"
	"strings"
	"sync"

	"github.com/spf13/viper"
	"golang.org/x/time/rate"

	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/key"
	"github.com/iyear/tdl/pkg/kv"
	"github.com/iyear/tdl/pkg/utils"
)

// settingsNS is a reserved kv namespace holding server-global web UI settings
// (upload rate, proxy). It is not an account: it never has a session, so the
// account list filters it out. sanitizeNamespace rejects it to prevent a login
// from colliding with it.
const settingsNS = "_web_settings"

// Settings holds the runtime-adjustable web server settings and persists them so
// they survive a restart. The upload rate limiter is shared by every upload and
// can be retuned live (so a change affects in-flight transfers). Changing the
// proxy reconnects all live clients.
type Settings struct {
	engine  kv.Storage
	clients *ClientManager

	mu        sync.RWMutex
	rateStr   string        // upload rate, human form ("5M", "" = unlimited), for display + persistence
	rateDlStr string        // download rate, same form
	proxy     string        // proxy URL, "" = direct
	limiter   *rate.Limiter // always non-nil; shared by uploads, retuned in place
	dlLimiter *rate.Limiter // always non-nil; shared by downloads (to-disk + stream), retuned in place
}

// loadSettings builds the Settings, reading any persisted values. Persisted
// values take precedence over the --rate / --proxy flags; when absent the flag
// value seeds the initial state. The effective proxy is written back to viper so
// clients created afterwards dial through it — call this before any client is
// started.
func loadSettings(ctx context.Context, engine kv.Storage, clients *ClientManager) *Settings {
	s := &Settings{engine: engine, clients: clients}

	// rate: persisted value overrides the flag
	rateStr := viper.GetString(consts.FlagRate)
	if v, ok := s.get(ctx, key.WebRate()); ok {
		rateStr = v
	}
	bps, err := utils.Byte.Parse(rateStr)
	if err != nil {
		bps, rateStr = 0, "" // a corrupt stored value falls back to unlimited
	}
	s.rateStr = rateStr
	s.limiter = utils.NewSharedRateLimiter(bps)

	// download rate: the shared --rate flag seeds it too (CLI semantics: one flag
	// for both directions); a persisted web value overrides
	rateDlStr := viper.GetString(consts.FlagRate)
	if v, ok := s.get(ctx, key.WebRateDl()); ok {
		rateDlStr = v
	}
	bpsDl, err := utils.Byte.Parse(rateDlStr)
	if err != nil {
		bpsDl, rateDlStr = 0, ""
	}
	s.rateDlStr = rateDlStr
	s.dlLimiter = utils.NewSharedRateLimiter(bpsDl)

	// proxy: persisted value overrides the flag; push the result to viper so the
	// long-lived clients (created right after) connect through it
	proxy := viper.GetString(consts.FlagProxy)
	if v, ok := s.get(ctx, key.WebProxy()); ok {
		proxy = v
	}
	s.proxy = proxy
	viper.Set(consts.FlagProxy, proxy)

	return s
}

func (s *Settings) get(ctx context.Context, k string) (string, bool) {
	kvd, err := s.engine.Open(settingsNS)
	if err != nil {
		return "", false
	}
	v, err := kvd.Get(ctx, k)
	if err != nil || len(v) == 0 {
		return "", false
	}
	return string(v), true
}

func (s *Settings) set(ctx context.Context, k, v string) error {
	kvd, err := s.engine.Open(settingsNS)
	if err != nil {
		return err
	}
	if v == "" {
		return kvd.Delete(ctx, k)
	}
	return kvd.Set(ctx, k, []byte(v))
}

// snapshot returns the current settings for display.
func (s *Settings) snapshot() (rateStr, rateDlStr, proxy string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rateStr, s.rateDlStr, s.proxy
}

// Limiter returns the shared upload limiter (always non-nil) to hand to up.Run.
func (s *Settings) Limiter() *rate.Limiter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.limiter
}

// DownLimiter returns the shared download limiter (always non-nil), used by both
// the to-disk path (dl.Run) and the browser streaming path.
func (s *Settings) DownLimiter() *rate.Limiter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dlLimiter
}

// setRate validates and applies a new upload rate (human form, "" = unlimited),
// retuning the shared limiter in place (in-flight uploads observe it) and
// persisting it. Returns an error if rateStr is not a valid size.
func (s *Settings) setRate(ctx context.Context, rateStr string) error {
	rateStr = strings.TrimSpace(rateStr)
	bps, err := utils.Byte.Parse(rateStr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	utils.SetRateLimit(s.limiter, bps)
	s.rateStr = rateStr
	s.mu.Unlock()
	return s.set(ctx, key.WebRate(), rateStr)
}

// setRateDl is setRate's counterpart for the download limiter (shared by
// in-flight to-disk downloads and browser streams alike).
func (s *Settings) setRateDl(ctx context.Context, rateStr string) error {
	rateStr = strings.TrimSpace(rateStr)
	bps, err := utils.Byte.Parse(rateStr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	utils.SetRateLimit(s.dlLimiter, bps)
	s.rateDlStr = rateStr
	s.mu.Unlock()
	return s.set(ctx, key.WebRateDl(), rateStr)
}

// setProxy applies a new proxy: persists it, pushes it to viper, and drops all
// live clients so the next operation reconnects through it. No-op (and no
// reconnect) when the value is unchanged.
func (s *Settings) setProxy(ctx context.Context, proxy string) (changed bool, err error) {
	proxy = strings.TrimSpace(proxy)
	s.mu.Lock()
	if proxy == s.proxy {
		s.mu.Unlock()
		return false, nil
	}
	s.proxy = proxy
	s.mu.Unlock()

	if err := s.set(ctx, key.WebProxy(), proxy); err != nil {
		return false, err
	}
	viper.Set(consts.FlagProxy, proxy)
	s.clients.DropAll()
	return true, nil
}
