package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gotd/contrib/http_io"
	"github.com/gotd/contrib/partio"
	"github.com/gotd/contrib/tg_io"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/spf13/viper"
	"golang.org/x/time/rate"

	"github.com/iyear/tdl/app/dl"
	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/consts"
)

// dlTemplate is the default download naming template (same as the CLI's `dl`
// default): "<dialog>_<message>_<filename>".
const dlTemplate = `{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}`

// handleDownloadStream streams a single message's media straight back to the
// browser (server never writes it to disk). It resolves the t.me link, opens a
// short-lived dc pool on the account's live client, and hands the request to the
// same partio streamer the CLI's `dl --serve` uses. The response is forced to a
// download (Content-Disposition: attachment) rather than inline preview.
//
// The URL is passed as a query param (?url=) rather than a path segment because a
// t.me link contains slashes; the browser hits this with a plain navigation / a
// hidden <a download>, so the response carries Content-Disposition: attachment.
func (s *Server) handleDownloadStream(w http.ResponseWriter, r *http.Request) {
	ns := mux.Vars(r)["ns"]
	link := strings.TrimSpace(r.URL.Query().Get("url"))
	if link == "" {
		writeErr(w, http.StatusBadRequest, errors.New("url is required"))
		return
	}

	lc, err := s.clients.Get(ns)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}

	// Resolve + stream on the account's live connection. do() serializes on the
	// account and ties the work to the HTTP request lifetime, so a canceled
	// download (browser closed) stops pulling from Telegram.
	err = lc.do(r.Context(), func(ctx context.Context, c *telegram.Client, kvd storage.Storage) error {
		pool := dcpool.NewPool(c,
			int64(viper.GetInt(consts.FlagPoolSize)),
			tclient.NewDefaultMiddlewares(ctx, viper.GetDuration(consts.FlagReconnectTimeout))...)
		defer func() { _ = pool.Close() }()

		item, err := resolveMedia(ctx, pool, kvd, link)
		if err != nil {
			return err
		}

		u := partio.NewStreamer(
			limitedChunkSource{
				inner:   tg_io.NewDownloader(pool.Client(ctx, item.DC)).ChunkSource(item.Size, item.InputFileLoc),
				limiter: s.settings.DownLimiter(),
			},
			int64(viper.GetInt(consts.FlagPartSize)))

		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, item.Name))
		http_io.NewHandler(u, item.Size).
			WithContentType(item.MIME).
			WithLog(logctx.From(ctx).Named("web-serve")).
			ServeHTTP(w, r)
		return nil
	})
	if err != nil {
		// The streamer may have already written headers/body; only a pre-stream
		// resolve error can still be reported as JSON.
		writeErr(w, http.StatusBadRequest, err)
	}
}

// downloadRequest is the body for a server-side (to-disk) download of one or more
// t.me links.
type downloadRequest struct {
	URLs []string `json:"urls"` // t.me message links
	Dir  string   `json:"dir"`  // absolute output directory on the server
}

// handleDownload downloads one or more t.me links to a directory on the server's
// filesystem, reusing the CLI downloader (naming template, resume, concurrency).
// It returns immediately with a task id; progress bars render on the server's
// terminal (same as uploads), and the web task only tracks queued/running/done.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	ns := mux.Vars(r)["ns"]

	var req downloadRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Dir = strings.TrimSpace(req.Dir)
	urls := cleanURLs(req.URLs)
	if len(urls) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("at least one url is required"))
		return
	}
	if req.Dir == "" {
		writeErr(w, http.StatusBadRequest, errors.New("output directory is required"))
		return
	}

	lc, err := s.clients.Get(ns)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}

	task := s.tasks.Create(ns, req.Dir, urls)
	files := make([]TaskFile, 0, len(urls))
	for _, u := range urls {
		files = append(files, TaskFile{Name: u, Size: -2})
	}
	s.tasks.Update(task.ID, func(t *Task) {
		t.Kind = "download"
		t.ChatName = req.Dir
		t.Files = files
	})

	opts := dl.Options{
		Dir:      req.Dir,
		Template: dlTemplate,
		URLs:     urls,
		Continue: true,
		Limiter:  s.settings.DownLimiter(),
	}

	s.startDownload(lc, task, opts)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": task.ID})
}

// startDownload runs a to-disk download in the background, mirroring startUpload:
// per-task cancelable context, status transitions, terminal progress bars.
func (s *Server) startDownload(lc *liveClient, task *Task, opts dl.Options) {
	ctx, cancel := context.WithCancel(lc.runCtx)
	s.tasks.SetCancel(task.ID, cancel)

	go func() {
		defer cancel()
		defer s.tasks.dropCancel(task.ID)

		s.tasks.Update(task.ID, func(t *Task) { t.Status = TaskRunning })

		err := lc.backgroundCtx(ctx, func(ctx context.Context, c *telegram.Client, kvd storage.Storage) error {
			return dl.Run(logctx.Named(ctx, "web-dl"), c, kvd, opts)
		})

		s.tasks.Update(task.ID, func(t *Task) {
			if err != nil {
				t.Status = TaskError
				t.Error = sendErrorDisplay(err)
				return
			}
			t.Status = TaskDone
			t.Progress = 100
		})
	}()
}

// limitedChunkSource throttles a partio.ChunkSource with the shared download
// limiter, so browser streams honor the web rate setting (the to-disk path is
// throttled separately, inside the core downloader's writeAt). The limiter is
// always non-nil (rate.Inf = unlimited) and retuned in place by the settings
// modal, so mid-stream rate changes take effect. Chunk size is --part-size
// (<= 1MiB), never above the limiter's burst floor, so WaitN cannot reject.
type limitedChunkSource struct {
	inner   partio.ChunkSource
	limiter *rate.Limiter
}

func (l limitedChunkSource) Chunk(ctx context.Context, offset int64, b []byte) (int64, error) {
	if err := l.limiter.WaitN(ctx, len(b)); err != nil {
		return 0, err
	}
	return l.inner.Chunk(ctx, offset, b)
}

// streamMedia carries the resolved media plus its MIME type for the streamer.
type streamMedia struct {
	*tmedia.Media
	MIME string
}

// resolveMedia turns a single t.me link into a downloadable media descriptor
// (location, size, dc, name, mime). It errors if the link points to a message
// with no downloadable media.
func resolveMedia(ctx context.Context, pool dcpool.Pool, kvd storage.Storage, link string) (*streamMedia, error) {
	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(pool.Default(ctx))

	peer, msgid, err := tutil.ParseMessageLink(ctx, manager, link)
	if err != nil {
		return nil, fmt.Errorf("parse link: %w", err)
	}

	msg, err := tutil.GetSingleMessage(ctx, pool.Default(ctx), peer.InputPeer(), msgid)
	if err != nil {
		return nil, fmt.Errorf("resolve message: %w", err)
	}

	md, ok := tmedia.GetMedia(msg)
	if !ok {
		return nil, errors.New("this message has no downloadable media")
	}

	mime := ""
	switch m := msg.Media.(type) {
	case *tg.MessageMediaDocument:
		if doc, ok := m.Document.AsNotEmpty(); ok {
			mime = doc.MimeType
		}
	case *tg.MessageMediaPhoto:
		mime = "image/jpeg"
	}

	return &streamMedia{Media: md, MIME: mime}, nil
}

// cleanURLs trims and drops empty entries.
func cleanURLs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, u := range in {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	return out
}
