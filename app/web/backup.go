package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/iyear/tdl/app/migrate"
)

// maxBackupSize caps an uploaded backup read into memory (defensive; real
// backups are KBs–single-digit MBs).
const maxBackupSize = 512 << 20 // 512MB

// handleBackup streams a zstd-compressed dump of all namespace data to the
// browser as a downloadable .tdl file. It reuses the same core as the CLI
// `backup` command, so the produced file is interchangeable with it.
//
// The dump is buffered first (backups are small — KBs to a few MB) so a failure
// surfaces as a clean JSON error instead of a truncated download.
//
// The backup is unencrypted and equivalent to account credentials.
func (s *Server) handleBackup(w http.ResponseWriter, _ *http.Request) {
	var buf bytes.Buffer
	if err := migrate.BackupTo(s.engine, &buf); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	name := fmt.Sprintf("%s.backup.tdl", time.Now().Format("2006-01-02-15_04_05"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = w.Write(buf.Bytes())
}

// handleRecover merges an uploaded .tdl backup into the current storage, then
// drops all live clients so accounts reconnect with the imported sessions. The
// merge is an upsert (keys in the backup overwrite same-named ones).
func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("expected multipart/form-data"))
		return
	}

	var (
		buf     bytes.Buffer
		gotFile bool
	)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if part.FormName() == "file" && part.FileName() != "" {
			if _, err := io.Copy(&buf, io.LimitReader(part, maxBackupSize)); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			gotFile = true
		}
	}

	if !gotFile || buf.Len() == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("no backup file"))
		return
	}

	if err := migrate.RecoverFrom(s.engine, &buf); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("恢复失败：%w", err))
		return
	}

	// Force every live client to be recreated so already-connected accounts pick
	// up the imported session on next use; newly imported accounts are lazily
	// created when first selected.
	s.clients.DropAll()

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
