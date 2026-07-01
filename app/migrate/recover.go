package migrate

import (
	"bytes"
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

// RecoverFrom reads a zstd-compressed backup from r and merges it into s. It's
// the storage-agnostic core shared by the CLI `recover` command and the web UI
// upload endpoint. The merge is an upsert: keys in the backup overwrite, keys
// absent from the backup are kept.
func RecoverFrom(s kv.Storage, r io.Reader) error {
	dec, err := zstd.NewReader(r)
	if err != nil {
		return errors.Wrap(err, "create zstd decoder")
	}
	defer dec.Close()

	metaB := bytes.NewBuffer(nil)
	if _, err = dec.WriteTo(metaB); err != nil {
		return err
	}

	var meta kv.Meta
	if err = json.Unmarshal(metaB.Bytes(), &meta); err != nil {
		return errors.Wrap(err, "unmarshal metadata")
	}

	if err = s.MigrateFrom(meta); err != nil {
		return errors.Wrap(err, "migrate from")
	}

	return nil
}

func Recover(ctx context.Context, file string) (rerr error) {
	f, err := os.Open(file)
	if err != nil {
		return errors.Wrap(err, "open file")
	}
	defer multierr.AppendInvoke(&rerr, multierr.Close(f))

	if err = RecoverFrom(kv.From(ctx), f); err != nil {
		return err
	}

	color.Green("Recover successfully, file: %s", file)
	return nil
}
