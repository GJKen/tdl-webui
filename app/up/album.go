package up

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/expr-lang/expr/vm"
	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/peers"
	gup "github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	pw "github.com/jedib0t/go-pretty/v6/progress"
	"github.com/samber/lo"
	"github.com/spf13/viper"

	"github.com/iyear/tdl/core/uploader"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/prog"
	"github.com/iyear/tdl/pkg/texpr"
	"github.com/iyear/tdl/pkg/utils"
)

// maxAlbum is the Telegram limit of media items per grouped message.
const maxAlbum = 10

// runAlbum sends the files as Telegram media group(s): files are uploaded
// sequentially (top-to-bottom) and each batch of up to 10 is sent as one
// grouped message. The caption goes on the first item of each group. Progress
// is reported to the optional web sink as a single aggregate per group, and to
// a single terminal tracker per group.
func runAlbum(
	ctx context.Context,
	client *tg.Client,
	manager *peers.Manager,
	files []*File,
	opts Options,
	caption *vm.Program,
	web uploader.Progress, // optional (web UI); nil for CLI
	pww pw.Writer,
) error {
	var (
		to  peers.Peer
		err error
	)
	if opts.Chat == "" {
		to, err = manager.Self(ctx)
	} else {
		to, err = tutil.GetInputPeer(ctx, manager, opts.Chat)
	}
	if err != nil {
		return errors.Wrap(err, "resolve peer")
	}

	threads := viper.GetInt(consts.FlagThreads)

	for start := 0; start < len(files); start += maxAlbum {
		end := start + maxAlbum
		if end > len(files) {
			end = len(files)
		}
		if err := sendAlbumGroup(ctx, client, to, files[start:end], opts, caption, web, pww, threads); err != nil {
			return err
		}
	}
	return nil
}

func sendAlbumGroup(
	ctx context.Context,
	client *tg.Client,
	to peers.Peer,
	group []*File,
	opts Options,
	caption *vm.Program,
	web uploader.Progress,
	pww pw.Writer,
	threads int,
) (rerr error) {
	// open the group's files (sequentially) and sum the bytes for progress
	ufs := make([]*uploaderFile, 0, len(group))
	defer func() {
		for _, uf := range ufs {
			_ = uf.Close()
		}
	}()

	var total int64
	for _, gf := range group {
		uf, err := openUploaderFile(gf.File)
		if err != nil {
			return err
		}
		ufs = append(ufs, uf)
		total += uf.size
	}

	var track *pw.Tracker
	if pww != nil {
		track = prog.AppendTracker(pww, utils.Byte.FormatBinaryBytes,
			fmt.Sprintf("album: %d files -> %s", len(group), to.VisibleName()), total)
	}
	fail := func(err error) error {
		if track != nil {
			track.MarkAsErrored()
		}
		return err
	}

	media := make([]message.MultiMediaOption, 0, len(group))
	var base int64
	for i, uf := range ufs {
		// upload each file, reporting cumulative group progress
		up := gup.NewUploader(client).
			WithPartSize(uploader.MaxPartSize).
			WithThreads(threads).
			WithProgress(&albumProgress{web: web, track: track, base: base, total: total})

		var caps []message.StyledTextOption // caption only on the first item
		if i == 0 {
			c, err := albumCaption(ctx, caption, group[i])
			if err != nil {
				return fail(err)
			}
			caps = c
		}

		m, err := uploader.BuildMedia(ctx, up, uf, nil, opts.Photo, opts.AsFile, caps...)
		if err != nil {
			return fail(errors.Wrap(err, "build media"))
		}
		media = append(media, m)
		base += uf.size
	}

	if _, err := message.NewSender(client).
		To(to.InputPeer()).
		Reply(opts.Thread).
		Album(ctx, media[0], media[1:]...); err != nil {
		return fail(errors.Wrap(err, "send album"))
	}

	if opts.Remove {
		for _, gf := range group {
			_ = os.Remove(gf.File)
		}
	}
	if track != nil {
		track.SetValue(total)
		track.MarkAsDone()
	}
	return nil
}

// albumProgress adapts the gotd per-file upload callback into a single aggregate
// for the group: base is the bytes already uploaded by earlier files in the
// group, total is the group's total bytes.
type albumProgress struct {
	web   uploader.Progress
	track *pw.Tracker
	base  int64
	total int64
}

func (p *albumProgress) Chunk(_ context.Context, state gup.ProgressState) error {
	cur := p.base + state.Uploaded
	if p.web != nil {
		p.web.OnUpload(nil, uploader.ProgressState{Uploaded: cur, Total: p.total})
	}
	if p.track != nil {
		p.track.SetValue(cur)
	}
	return nil
}

// albumCaption evaluates the caption program for the file and returns the
// styled caption option(s) — an empty slice when the caption is empty.
func albumCaption(ctx context.Context, program *vm.Program, file *File) ([]message.StyledTextOption, error) {
	res, err := texpr.Run(program, exprEnv(ctx, file))
	if err != nil {
		return nil, errors.Wrap(err, "run caption")
	}
	s, _ := res.(string)
	if s == "" {
		return nil, nil
	}

	b := &entity.Builder{}
	if err := html.HTML(strings.NewReader(s), b, html.Options{}); err != nil {
		return nil, errors.Wrap(err, "parse caption html")
	}
	msg, ents := b.Complete()
	opt := styling.Custom(func(eb *entity.Builder) error {
		eb.Format(msg, lo.Map(ents, func(it tg.MessageEntityClass, _ int) entity.Formatter {
			return func(_, _ int) tg.MessageEntityClass { return it }
		})...)
		return nil
	})
	return []message.StyledTextOption{opt}, nil
}

func openUploaderFile(path string) (*uploaderFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "open file")
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, errors.Wrap(err, "stat file")
	}
	return &uploaderFile{File: f, size: stat.Size()}, nil
}
