package key

import (
	"github.com/iyear/tdl/core/storage/keygen"
)

func App() string {
	return keygen.New("app")
}

// Session is the key (within a namespace) under which the Telegram MTProto
// session is stored; its presence means the namespace is logged in. Must match
// core/storage.(*Session).key().
func Session() string {
	return keygen.New("session")
}

func Resume(fingerprint string) string {
	return keygen.New("resume", fingerprint)
}

// WebAlias is the key (within a namespace) for the web UI's editable display
// name. It does not touch the namespace itself, so the CLI keeps working.
func WebAlias() string {
	return keygen.New("web", "alias")
}

// WebSelf is the key (within a namespace) for the web UI's cached account
// identity (id/username/name), written on a successful login so the account
// list can show it without reconnecting to Telegram.
func WebSelf() string {
	return keygen.New("web", "self")
}

// WebChats is the key (within a namespace) for the web UI's cached dialog list
// (the upload target picker), so it survives a server restart without a fresh
// Telegram fetch.
func WebChats() string {
	return keygen.New("web", "chats")
}

// WebRate and WebProxy are server-global web UI settings (upload rate limit and
// proxy), stored under a reserved settings namespace — not a real account — so
// they persist across restarts.
func WebRate() string {
	return keygen.New("web", "rate")
}

func WebProxy() string {
	return keygen.New("web", "proxy")
}
