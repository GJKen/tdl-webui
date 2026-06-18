package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gotd/td/telegram"

	"github.com/iyear/tdl/app/chat"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/key"
)

// chatsCache is the dialog list persisted in the namespace KV so the target
// picker survives server restarts without a fresh Telegram fetch.
type chatsCache struct {
	UpdatedAt string         `json:"updated_at"`
	Dialogs   []*chat.Dialog `json:"dialogs"`
}

// handleChats serves the account's dialogs for the target picker.
//
// Default (no ?refresh): returns the locally cached list from the namespace KV
// without connecting to Telegram, so the dropdown is populated instantly on
// startup. ?refresh=1 does a live fetch, re-caches it, and returns it. The live
// fetch also caches peer access-hashes (via chat.ListDialogs) so a private
// chat/channel can subsequently be uploaded to by numeric id.
func (s *Server) handleChats(w http.ResponseWriter, r *http.Request) {
	ns := mux.Vars(r)["ns"]

	// cache hit path: read straight from local storage, never connect.
	if r.URL.Query().Get("refresh") == "" {
		kvd, err := s.engine.Open(ns)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if b, err := kvd.Get(r.Context(), key.WebChats()); err == nil && len(b) > 0 {
			// Re-filter on read: an old cache (written before deleted-account /
			// self filtering existed) may still hold deleted accounts or the
			// account's own dialog; drop them here so they disappear without
			// needing a manual "refresh chats".
			var cached chatsCache
			if json.Unmarshal(b, &cached) == nil {
				cached.Dialogs = dropSelfDialog(dropDeletedDialogs(cached.Dialogs), selfPeerID(r.Context(), kvd))
				writeJSON(w, http.StatusOK, cached)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b)
			return
		}
		writeJSON(w, http.StatusOK, chatsCache{Dialogs: []*chat.Dialog{}})
		return
	}

	filter := strings.TrimSpace(r.URL.Query().Get("filter"))
	if filter == "" {
		filter = "true"
	}

	lc, err := s.clients.Get(ns)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}

	var resp chatsCache
	err = lc.do(r.Context(), func(ctx context.Context, c *telegram.Client, kvd storage.Storage) error {
		d, err := chat.ListDialogs(ctx, c, kvd, filter)
		if err != nil {
			return err
		}
		d = dropDeletedDialogs(d) // also drop deleted accounts whose flag wasn't set in the dialog list

		// Drop the account's own dialog: the web UI always renders a synthetic
		// "Saved Messages" entry for self, so keeping the real self dialog would
		// show the same destination twice. Prefer the cached id; fall back to a
		// live Self() lookup (and cache it) for accounts that never cached it.
		sid := selfPeerID(ctx, kvd)
		if sid == 0 {
			if u, sErr := c.Self(ctx); sErr == nil {
				sid = u.ID
				cacheSelf(ctx, kvd, &SelfInfo{ID: u.ID, Username: u.Username, FirstName: u.FirstName, LastName: u.LastName})
			}
		}
		d = dropSelfDialog(d, sid)

		resp = chatsCache{UpdatedAt: time.Now().Format(time.RFC3339), Dialogs: d}
		// best-effort persist for the next startup, on the same serialized op
		if b, mErr := json.Marshal(resp); mErr == nil {
			_ = kvd.Set(ctx, key.WebChats(), b)
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// dropDeletedDialogs removes deleted Telegram accounts that may linger in an old
// cache written before deleted-account filtering existed. A deleted account is a
// private (non-bot) peer that has lost its name and username.
func dropDeletedDialogs(dialogs []*chat.Dialog) []*chat.Dialog {
	out := make([]*chat.Dialog, 0, len(dialogs))
	for _, d := range dialogs {
		if d.Type == chat.DialogPrivate && !d.Bot &&
			strings.TrimSpace(d.VisibleName) == "" && strings.TrimSpace(d.Username) == "" {
			continue
		}
		out = append(out, d)
	}
	return out
}

// selfPeerID returns the account's own user id from the cached self info, or 0
// if it hasn't been cached yet (e.g. a CLI-created account never viewed).
func selfPeerID(ctx context.Context, kvd storage.Storage) int64 {
	b, err := kvd.Get(ctx, key.WebSelf())
	if err != nil || len(b) == 0 {
		return 0
	}
	var self SelfInfo
	if json.Unmarshal(b, &self) != nil {
		return 0
	}
	return self.ID
}

// dropSelfDialog removes the account's own private chat (Saved Messages). The
// web UI renders a synthetic "Saved Messages" entry for it, so leaving the real
// self dialog in the list would show the same destination twice. selfID==0
// (unknown) leaves the list untouched.
func dropSelfDialog(dialogs []*chat.Dialog, selfID int64) []*chat.Dialog {
	if selfID == 0 {
		return dialogs
	}
	out := make([]*chat.Dialog, 0, len(dialogs))
	for _, d := range dialogs {
		if d.Type == chat.DialogPrivate && d.ID == selfID {
			continue
		}
		out = append(out, d)
	}
	return out
}
