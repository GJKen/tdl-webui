package chat

import (
	"context"
	"sort"

	"github.com/gotd/td/tg"
)

const (
	peerKindUser    = "user"
	peerKindChat    = "chat"
	peerKindChannel = "channel"
)

type peerKey struct {
	kind string
	id   int64
}

func ListDialogFolders(ctx context.Context, api *tg.Client, dialogs []*Dialog) ([]*DialogFolder, error) {
	res, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, err
	}

	for _, d := range dialogs {
		d.FolderIDs = nil
	}

	folders := make([]*DialogFolder, 0, len(res.Filters))
	for _, filter := range res.Filters {
		switch f := filter.(type) {
		case *tg.DialogFilter:
			folder := dialogFilterMeta(f.ID, f.Title.Text, f.Emoticon, f.Color, f.PinnedPeers)
			folders = append(folders, folder)
			for _, d := range dialogs {
				if dialogMatchesFilter(d, f) {
					d.FolderIDs = append(d.FolderIDs, f.ID)
				}
			}
		case *tg.DialogFilterChatlist:
			folder := dialogFilterMeta(f.ID, f.Title.Text, f.Emoticon, f.Color, f.PinnedPeers)
			folders = append(folders, folder)
			for _, d := range dialogs {
				if dialogMatchesChatlist(d, f) {
					d.FolderIDs = append(d.FolderIDs, f.ID)
				}
			}
		}
	}

	for _, d := range dialogs {
		sort.Ints(d.FolderIDs)
	}

	return folders, nil
}

func dialogFilterMeta(id int, title, emoticon string, color int, pinned []tg.InputPeerClass) *DialogFolder {
	return &DialogFolder{
		ID:          id,
		Title:       title,
		Emoticon:    emoticon,
		Color:       color,
		PinnedPeers: folderPeers(pinned),
	}
}

func folderPeers(peers []tg.InputPeerClass) []FolderPeer {
	out := make([]FolderPeer, 0, len(peers))
	for _, p := range peers {
		if key, ok := inputPeerKey(p); ok {
			out = append(out, FolderPeer{PeerKind: key.kind, ID: key.id})
		}
	}
	return out
}

func dialogMatchesFilter(d *Dialog, f *tg.DialogFilter) bool {
	if d == nil || f == nil {
		return false
	}

	key, ok := dialogPeerKey(d)
	if !ok {
		return false
	}
	if peerSetHas(peerSet(f.ExcludePeers), key) {
		return false
	}
	if f.ExcludeArchived && d.Archived {
		return false
	}
	if peerSetHas(peerSet(f.PinnedPeers), key) || peerSetHas(peerSet(f.IncludePeers), key) {
		return true
	}

	return dialogMatchesAutoFilter(d, f)
}

func dialogMatchesChatlist(d *Dialog, f *tg.DialogFilterChatlist) bool {
	if d == nil || f == nil {
		return false
	}

	key, ok := dialogPeerKey(d)
	if !ok {
		return false
	}
	return peerSetHas(peerSet(f.PinnedPeers), key) || peerSetHas(peerSet(f.IncludePeers), key)
}

func dialogMatchesAutoFilter(d *Dialog, f *tg.DialogFilter) bool {
	switch d.PeerKind {
	case peerKindUser:
		if d.Bot {
			return f.Bots
		}
		if d.Contact {
			return f.Contacts
		}
		return f.NonContacts
	case peerKindChat:
		return f.Groups
	case peerKindChannel:
		switch d.Type {
		case DialogGroup:
			return f.Groups
		case DialogChannel:
			return f.Broadcasts
		}
	}

	switch d.Type {
	case DialogPrivate:
		if d.Bot {
			return f.Bots
		}
		if d.Contact {
			return f.Contacts
		}
		return f.NonContacts
	case DialogGroup:
		return f.Groups
	case DialogChannel:
		return f.Broadcasts
	default:
		return false
	}
}

func peerSet(peers []tg.InputPeerClass) map[peerKey]struct{} {
	out := make(map[peerKey]struct{}, len(peers))
	for _, p := range peers {
		if k, ok := inputPeerKey(p); ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func peerSetHas(set map[peerKey]struct{}, key peerKey) bool {
	_, ok := set[key]
	return ok
}

func inputPeerKey(p tg.InputPeerClass) (peerKey, bool) {
	switch v := p.(type) {
	case *tg.InputPeerUser:
		return peerKey{kind: peerKindUser, id: v.UserID}, true
	case *tg.InputPeerUserFromMessage:
		return peerKey{kind: peerKindUser, id: v.UserID}, true
	case *tg.InputPeerChat:
		return peerKey{kind: peerKindChat, id: v.ChatID}, true
	case *tg.InputPeerChannel:
		return peerKey{kind: peerKindChannel, id: v.ChannelID}, true
	case *tg.InputPeerChannelFromMessage:
		return peerKey{kind: peerKindChannel, id: v.ChannelID}, true
	default:
		return peerKey{}, false
	}
}

func dialogPeerKey(d *Dialog) (peerKey, bool) {
	if d == nil || d.ID == 0 {
		return peerKey{}, false
	}
	switch d.PeerKind {
	case peerKindUser, peerKindChat, peerKindChannel:
		return peerKey{kind: d.PeerKind, id: d.ID}, true
	}
	switch d.Type {
	case DialogPrivate:
		return peerKey{kind: peerKindUser, id: d.ID}, true
	case DialogGroup:
		return peerKey{kind: peerKindChat, id: d.ID}, true
	case DialogChannel:
		return peerKey{kind: peerKindChannel, id: d.ID}, true
	default:
		return peerKey{}, false
	}
}

func dialogArchived(d *tg.Dialog) bool {
	if d == nil {
		return false
	}
	id, ok := d.GetFolderID()
	return ok && id != 0
}
