package migrate

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/klauspost/compress/zstd"
	"go.uber.org/multierr"

	"github.com/iyear/tdl/pkg/kv"
)

// BackupTo reads all data from s, JSON-encodes it and writes it zstd-compressed
// to w. It's the storage-agnostic core shared by the CLI `backup` command and
// the web UI download endpoint.
func BackupTo(s kv.Storage, w io.Writer) (rerr error) {
	meta, err := s.MigrateTo()
	if err != nil {
		return errors.Wrap(err, "read metadata")
	}

	enc, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return errors.Wrap(err, "create zstd encoder")
	}
	defer multierr.AppendInvoke(&rerr, multierr.Close(enc))

	metaB, err := json.Marshal(meta)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	if _, err = enc.Write(metaB); err != nil {
		return errors.Wrap(err, "write metadata")
	}

	return nil
}

func Backup(ctx context.Context, dst string) (rerr error) {
	f, err := os.Create(dst)
	if err != nil {
		return errors.Wrap(err, "create file")
	}
	defer multierr.AppendInvoke(&rerr, multierr.Close(f))

	if err = BackupTo(kv.From(ctx), f); err != nil {
		return err
	}

	color.Green("Backup successfully, file: %s", dst)
	return nil
}
