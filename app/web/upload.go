package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gotd/td/telegram"

	"github.com/iyear/tdl/app/up"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
)

type uploadRequest struct {
	Path     string   `json:"path"`      // file or directory on the server's filesystem
	To       string   `json:"to"`        // target: chat id / @username / empty = Saved Messages
	ChatID   int64    `json:"chat_id"`   // numeric id of the picked conversation (for grouping); 0 if unknown
	ChatName string   `json:"chat_name"` // display name of the picked conversation (for the window title/preview)
	Topic    int      `json:"topic"`     // topic id within a forum group (requires To)
	Caption  string   `json:"caption"`   // markdown; empty = no caption
	Photo    bool     `json:"photo"`     // send images as photos instead of files
	AsFile   bool     `json:"file"`      // force a plain document (skip photo/video/audio detection)
	Includes []string `json:"includes"`  // only these extensions
	Excludes []string `json:"excludes"`  // skip these extensions
}

// statFiles summarizes each top-level path for display in the chat bubbles:
// a file gets its byte size, a directory is marked -1 (batch, no single size),
// and a stat failure is marked -2 (unknown).
func statFiles(paths []string) []TaskFile {
	out := make([]TaskFile, 0, len(paths))
	for _, p := range paths {
		tf := TaskFile{Name: filepath.Base(p), Size: -2}
		if info, err := os.Stat(p); err == nil {
			if info.IsDir() {
				tf.Size = -1
			} else {
				tf.Size = info.Size()
			}
		}
		out = append(out, tf)
	}
	return out
}

// buildCaption turns the web's Markdown caption box into the HTML the upload
// pipeline expects, wrapped as an expr string literal so it survives expr
// resolution verbatim (app/up resolves the caption via expr, then HTML-parses
// the result). Empty stays empty (no caption).
func buildCaption(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	return exprStringLiteral(markdownToTelegramHTML(md))
}

// resolveChatID prefers an explicit numeric id; otherwise falls back to a
// numeric target string. Returns 0 when neither is numeric (e.g. @username,
// Saved Messages).
func resolveChatID(explicit int64, to string) int64 {
	if explicit != 0 {
		return explicit
	}
	if to != "" {
		if n, err := strconv.ParseInt(to, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// handleUpload sends a file/dir that already lives on the server's filesystem
// (the "server local path" mode). The browser only passes a path.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ns := mux.Vars(r)["ns"]

	var req uploadRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	req.To = strings.TrimSpace(req.To)

	if req.Path == "" {
		writeErr(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	if req.Topic != 0 && req.To == "" {
		writeErr(w, http.StatusBadRequest, errors.New("topic requires a target chat"))
		return
	}
	if len(req.Includes) > 0 && len(req.Excludes) > 0 {
		writeErr(w, http.StatusBadRequest, errors.New("includes and excludes are mutually exclusive"))
		return
	}

	lc, err := s.clients.Get(ns)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}

	target := req.To
	if target == "" {
		target = "Saved Messages"
	}
	chatName := strings.TrimSpace(req.ChatName)
	if chatName == "" {
		chatName = target
	}

	task := s.tasks.Create(ns, target, []string{req.Path})
	s.tasks.Update(task.ID, func(t *Task) {
		t.ChatID = resolveChatID(req.ChatID, req.To)
		t.ChatName = chatName
		t.Files = statFiles([]string{req.Path})
		t.Caption = req.Caption
	})

	opts := up.Options{
		Chat:     req.To,
		Thread:   req.Topic,
		Paths:    []string{req.Path},
		Includes: req.Includes,
		Excludes: req.Excludes,
		Photo:    req.Photo,
		AsFile:   req.AsFile,
		Caption:  buildCaption(req.Caption),
	}

	s.startUpload(lc, task, opts, "")
	writeJSON(w, http.StatusOK, map[string]string{"task_id": task.ID})
}

// handleUploadFiles receives a single file's bytes from the browser (drag-drop /
// file picker), stages it under a unique temp dir, and sends it. The staged dir
// is removed once the upload finishes. The browser sends one file per request
// (so each becomes its own task/bubble); non-file fields carry the options.
func (s *Server) handleUploadFiles(w http.ResponseWriter, r *http.Request) {
	ns := mux.Vars(r)["ns"]

	lc, err := s.clients.Get(ns)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("expected multipart/form-data"))
		return
	}

	fields := map[string]string{}
	var (
		stagedDir, stagedPath, origName string
		stagedSize                      int64
		gotFile                         bool
	)
	cleanup := func() {
		if stagedDir != "" {
			_ = os.RemoveAll(stagedDir)
		}
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			writeErr(w, http.StatusBadRequest, err)
			return
		}

		if part.FormName() == "file" && part.FileName() != "" {
			origName = filepath.Base(part.FileName())
			if origName == "" || origName == "." || origName == ".." {
				cleanup()
				writeErr(w, http.StatusBadRequest, errors.New("invalid file name"))
				return
			}
			dir, err := os.MkdirTemp(ensureStagingRoot(), "up-*")
			if err != nil {
				cleanup()
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			stagedDir = dir
			stagedPath = filepath.Join(dir, origName)
			f, err := os.Create(stagedPath)
			if err != nil {
				cleanup()
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			n, cErr := io.Copy(f, part)
			_ = f.Close()
			if cErr != nil {
				cleanup()
				writeErr(w, http.StatusInternalServerError, cErr)
				return
			}
			stagedSize = n
			gotFile = true
		} else {
			// a regular form field (cap its size defensively)
			buf, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			fields[part.FormName()] = string(buf)
		}
	}

	if !gotFile {
		cleanup()
		writeErr(w, http.StatusBadRequest, errors.New("no file part"))
		return
	}

	to := strings.TrimSpace(fields["to"])
	topic, _ := strconv.Atoi(strings.TrimSpace(fields["topic"]))
	if topic != 0 && to == "" {
		cleanup()
		writeErr(w, http.StatusBadRequest, errors.New("topic requires a target chat"))
		return
	}

	target := to
	if target == "" {
		target = "Saved Messages"
	}
	chatName := strings.TrimSpace(fields["chat_name"])
	if chatName == "" {
		chatName = target
	}

	task := s.tasks.Create(ns, target, []string{stagedPath})
	s.tasks.Update(task.ID, func(t *Task) {
		t.ChatID = resolveChatID(parseInt64(fields["chat_id"]), to)
		t.ChatName = chatName
		t.Files = []TaskFile{{Name: origName, Size: stagedSize}}
		t.Caption = fields["caption"]
	})

	opts := up.Options{
		Chat:    to,
		Thread:  topic,
		Paths:   []string{stagedPath},
		Photo:   isTrue(fields["photo"]),
		AsFile:  isTrue(fields["as_file"]),
		Caption: buildCaption(fields["caption"]),
	}

	s.startUpload(lc, task, opts, stagedDir)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": task.ID})
}

// handleUploadAlbum receives several files from the browser and sends them as a
// single Telegram media group (album). All files are staged under one temp dir
// (each in its own subdir to keep original names), tracked by one task, and
// removed once the upload finishes. The browser sends <=10 files per request.
func (s *Server) handleUploadAlbum(w http.ResponseWriter, r *http.Request) {
	ns := mux.Vars(r)["ns"]

	lc, err := s.clients.Get(ns)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("expected multipart/form-data"))
		return
	}

	stagedDir, err := os.MkdirTemp(ensureStagingRoot(), "album-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cleanup := func() { _ = os.RemoveAll(stagedDir) }

	fields := map[string]string{}
	var (
		paths []string
		files []TaskFile
		idx   int
	)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			writeErr(w, http.StatusBadRequest, err)
			return
		}

		if part.FormName() == "file" && part.FileName() != "" {
			name := filepath.Base(part.FileName())
			if name == "" || name == "." || name == ".." {
				cleanup()
				writeErr(w, http.StatusBadRequest, errors.New("invalid file name"))
				return
			}
			// each file in its own subdir so original names never collide, and
			// the order (subdir index) is preserved -> album stays top-to-bottom
			sub := filepath.Join(stagedDir, strconv.Itoa(idx))
			if err := os.MkdirAll(sub, 0o700); err != nil {
				cleanup()
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			p := filepath.Join(sub, name)
			f, err := os.Create(p)
			if err != nil {
				cleanup()
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			n, cErr := io.Copy(f, part)
			_ = f.Close()
			if cErr != nil {
				cleanup()
				writeErr(w, http.StatusInternalServerError, cErr)
				return
			}
			paths = append(paths, p)
			files = append(files, TaskFile{Name: name, Size: n})
			idx++
		} else {
			buf, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			fields[part.FormName()] = string(buf)
		}
	}

	if len(paths) == 0 {
		cleanup()
		writeErr(w, http.StatusBadRequest, errors.New("no files"))
		return
	}

	to := strings.TrimSpace(fields["to"])
	topic, _ := strconv.Atoi(strings.TrimSpace(fields["topic"]))
	if topic != 0 && to == "" {
		cleanup()
		writeErr(w, http.StatusBadRequest, errors.New("topic requires a target chat"))
		return
	}

	target := to
	if target == "" {
		target = "Saved Messages"
	}
	chatName := strings.TrimSpace(fields["chat_name"])
	if chatName == "" {
		chatName = target
	}

	task := s.tasks.Create(ns, target, paths)
	s.tasks.Update(task.ID, func(t *Task) {
		t.ChatID = resolveChatID(parseInt64(fields["chat_id"]), to)
		t.ChatName = chatName
		t.Files = files
		t.Caption = fields["caption"]
		t.Album = true
	})

	// as_file forces every item to a plain document so a type-mixed selection
	// (e.g. an image + a non-media file) becomes one valid all-document album —
	// Telegram rejects a group that mixes photos/videos with documents.
	asFile := isTrue(fields["as_file"])
	opts := up.Options{
		Chat:    to,
		Thread:  topic,
		Paths:   paths,
		Photo:   !asFile, // album: images as photos, videos as videos, others as documents
		AsFile:  asFile,
		Album:   true,
		Caption: buildCaption(fields["caption"]),
	}

	s.startUpload(lc, task, opts, stagedDir)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": task.ID})
}

// startUpload runs an upload in the background: it reports progress into the
// task, supports per-task cancellation, updates status, and removes cleanupDir
// (staged browser bytes, if any) once it finishes.
func (s *Server) startUpload(lc *liveClient, task *Task, opts up.Options, cleanupDir string) {
	opts.Progress = &webProgress{tasks: s.tasks, id: task.ID}
	// Hand every upload the shared, live-adjustable limiter so the web rate
	// setting applies (and a rate change retunes in-flight transfers too).
	opts.Limiter = s.settings.Limiter()

	// A per-task cancelable context (derived from the account's live context) so
	// a single upload can be canceled — even while queued behind another.
	ctx, cancel := context.WithCancel(lc.runCtx)
	s.tasks.SetCancel(task.ID, cancel)

	go func() {
		defer cancel()
		defer s.tasks.dropCancel(task.ID)
		if cleanupDir != "" {
			defer func() { _ = os.RemoveAll(cleanupDir) }()
		}

		s.tasks.Update(task.ID, func(t *Task) { t.Status = TaskRunning })

		err := lc.backgroundCtx(ctx, func(ctx context.Context, c *telegram.Client, kvd storage.Storage) error {
			return up.Run(logctx.Named(ctx, "web-up"), c, kvd, opts)
		})

		s.tasks.Update(task.ID, func(t *Task) {
			if err != nil {
				t.Status = TaskError
				t.Error = sendErrorDisplay(err)
				return
			}
			// A per-file failure may already have been recorded via the progress
			// sink's OnDone (the non-album path keeps up.Run's error nil to let a
			// batch continue); don't overwrite it with a false "done".
			if t.Status == TaskError {
				return
			}
			t.Status = TaskDone
			t.Progress = 100
		})
	}()
}

// ensureStagingRoot returns (creating if needed) the staging dir for browser
// uploads, under the OS temp dir.
func ensureStagingRoot() string {
	root := filepath.Join(os.TempDir(), "tdl-web-upload")
	_ = os.MkdirAll(root, 0o700)
	return root
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func isTrue(s string) bool { return s == "true" || s == "1" }
