package web

import (
	"strings"

	"github.com/gotd/td/tgerr"
)

// permissionErrTypes are Telegram RPC error types that all mean the same thing
// to a user: "you are not allowed to send here" (no send permission / blocked /
// banned / no access to the peer).
var permissionErrTypes = []string{
	"CHAT_WRITE_FORBIDDEN",
	"CHAT_SEND_MEDIA_FORBIDDEN",
	"CHAT_SEND_PHOTOS_FORBIDDEN",
	"CHAT_SEND_VIDEOS_FORBIDDEN",
	"CHAT_SEND_DOCS_FORBIDDEN",
	"CHAT_SEND_AUDIOS_FORBIDDEN",
	"CHAT_SEND_VOICES_FORBIDDEN",
	"CHAT_SEND_GIFS_FORBIDDEN",
	"CHAT_SEND_STICKERS_FORBIDDEN",
	"USER_IS_BLOCKED",
	"USER_BANNED_IN_CHANNEL",
	"USER_PRIVACY_RESTRICTED",
	"CHANNEL_PRIVATE",
}

// sendErrorDisplay turns an upload/send failure into the message shown on the
// task bubble. Permission-type Telegram errors (e.g. CHAT_SEND_MEDIA_FORBIDDEN)
// are translated to a clear "无权限发送（<code>）"; anything else passes through
// unchanged. This is the reactive fallback for sends that the proactive
// per-dialog permission check (chat.Dialog.SendForbidden) didn't catch — e.g. a
// stale chat cache, or a private peer that blocked us (undetectable up front).
func sendErrorDisplay(err error) string {
	if err == nil {
		return ""
	}
	if tgerr.Is(err, permissionErrTypes...) {
		if e, ok := tgerr.As(err); ok && e.Type != "" {
			return "无权限发送（" + e.Type + "）"
		}
		return "无权限发送"
	}
	// substring fallback: gotd wraps RPC errors; if the typed error is ever lost
	// from the Unwrap chain, still catch the obvious markers in the text.
	up := strings.ToUpper(err.Error())
	for _, t := range permissionErrTypes {
		if strings.Contains(up, t) {
			return "无权限发送（" + t + "）"
		}
	}
	return err.Error()
}
