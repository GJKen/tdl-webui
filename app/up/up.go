package up

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/fatih/color"
	"github.com/gabriel-vasile/mimetype"
	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/spf13/viper"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/core/uploader"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/prog"
	"github.com/iyear/tdl/pkg/texpr"
	"github.com/iyear/tdl/pkg/utils"
)

type Options struct {
	Chat     string
	Thread   int
	To       string
	Paths    []string
	Includes []string
	Excludes []string
	Remove   bool
	Photo    bool
	AsFile   bool
	Album    bool
	Caption  string
	// Progress, when set, additionally receives upload progress callbacks (the
	// terminal progress bar always runs too). Used by the web UI to report
	// per-task percent.
	Progress uploader.Progress
	// Limiter, when non-nil, throttles upload read throughput and overrides the
	// limiter built from the --rate flag. The web UI passes a single shared
	// limiter it adjusts live, so its rate setting (not the CLI flag) applies and
	// changes affect in-flight uploads too.
	Limiter *rate.Limiter
}

// multiProgress fans uploader progress callbacks out to several sinks.
type multiProgress []uploader.Progress

func (m multiProgress) OnAdd(e uploader.Elem) {
	for _, p := range m {
		p.OnAdd(e)
	}
}

func (m multiProgress) OnUpload(e uploader.Elem, s uploader.ProgressState) {
	for _, p := range m {
		p.OnUpload(e, s)
	}
}

func (m multiProgress) OnDone(e uploader.Elem, err error) {
	for _, p := range m {
		p.OnDone(e, err)
	}
}

type Env struct {
	FilePath  string `comment:"File path"`
	FileName  string `comment:"File name"`
	FileExt   string `comment:"File extension"`
	ThumbPath string `comment:"Thumbnail path"`
	MIME      string `comment:"File mime type"`
}

func Run(ctx context.Context, c *telegram.Client, kvd storage.Storage, opts Options) (rerr error) {
	if opts.To == "-" || opts.Caption == "-" {
		fg := texpr.NewFieldsGetter(nil)

		fields, err := fg.Walk(exprEnv(context.Background(), nil))
		if err != nil {
			return fmt.Errorf("failed to walk fields: %w", err)
		}

		fmt.Print(fg.Sprint(fields, true))
		return nil
	}

	files, err := walk(opts.Paths, opts.Includes, opts.Excludes)
	if err != nil {
		return err
	}

	color.Blue("Files count: %d", len(files))

	pool := dcpool.NewPool(c,
		int64(viper.GetInt(consts.FlagPoolSize)),
		tclient.NewDefaultMiddlewares(ctx, viper.GetDuration(consts.FlagReconnectTimeout))...)
	defer multierr.AppendInvoke(&rerr, multierr.Close(pool))

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(pool.Default(ctx))

	to, err := resolveDest(ctx, manager, opts.To)
	if err != nil {
		return errors.Wrap(err, "get target peer")
	}

	caption, err := resolveCaption(ctx, opts.Caption)
	if err != nil {
		return errors.Wrap(err, "get caption")
	}

	upProgress := prog.New(utils.Byte.FormatBinaryBytes)
	upProgress.SetNumTrackersExpected(len(files))
	if !viper.GetBool(consts.FlagDisableProgressPS) {
		prog.EnablePS(ctx, upProgress)
	}

	rateVal, err := utils.Byte.Parse(viper.GetString(consts.FlagRate))
	if err != nil {
		return errors.Wrap(err, "parse rate")
	}
	limiter := utils.NewRateLimiter(rateVal)
	// The web UI injects a shared, live-adjustable limiter; when set it overrides
	// the flag-built one so the web's own rate setting applies (and mid-flight
	// rate changes are observed by the in-progress read throttle).
	if opts.Limiter != nil {
		limiter = opts.Limiter
	}

	logctx.From(ctx).Info("Start upload",
		zap.Int("files", len(files)),
		zap.Int("threads", viper.GetInt(consts.FlagThreads)),
		zap.Int("limit", viper.GetInt(consts.FlagLimit)),
		zap.Bool("album", opts.Album),
		zap.Int64("rate", rateVal))

	// Album mode: send the files as Telegram media group(s) (one grouped message
	// per <=10 files) instead of one message per file. Handled separately from
	// the per-file uploader iterator path.
	if opts.Album {
		go upProgress.Render()
		defer prog.Wait(ctx, upProgress)
		return runAlbum(ctx, pool.Default(ctx), manager, files, opts, caption, opts.Progress, upProgress, limiter)
	}

	// terminal progress always renders; an optional extra sink (e.g. web) is
	// composed in when provided.
	var progress uploader.Progress = newProgress(upProgress)
	if opts.Progress != nil {
		progress = multiProgress{newProgress(upProgress), opts.Progress}
	}

	options := uploader.Options{
		Client:   pool.Default(ctx),
		Threads:  viper.GetInt(consts.FlagThreads),
		Iter:     newIter(files, to, caption, opts.Chat, opts.Thread, opts.Photo, opts.AsFile, opts.Remove, viper.GetDuration(consts.FlagDelay), manager),
		Progress: progress,
		Limiter:  limiter,
	}

	up := uploader.New(options)

	go upProgress.Render()
	defer prog.Wait(ctx, upProgress)

	return up.Upload(ctx, viper.GetInt(consts.FlagLimit))
}

func resolveDest(ctx context.Context, manager *peers.Manager, input string) (*vm.Program, error) {
	compile := func(i string) (*vm.Program, error) {
		return expr.Compile(i, expr.Env(exprEnv(ctx, nil)))
	}

	if input == "" {
		return compile(`""`)
	}

	if exp, err := os.ReadFile(input); err == nil {
		return compile(string(exp))
	}

	if _, err := tutil.GetInputPeer(ctx, manager, input); err == nil {
		return compile(fmt.Sprintf(`"%s"`, input))
	}

	return compile(input)
}

func resolveCaption(ctx context.Context, input string) (*vm.Program, error) {
	compile := func(i string) (*vm.Program, error) {
		// we pass empty peer and message to enable type checking
		return expr.Compile(i, expr.Env(exprEnv(ctx, nil)), expr.AsKind(reflect.String))
	}

	// default
	if input == "" {
		return compile(`""`)
	}

	// file
	if exp, err := os.ReadFile(input); err == nil {
		return compile(string(exp))
	}

	// text
	return compile(input)
}

func exprEnv(ctx context.Context, file *File) Env {
	if file == nil {
		return Env{}
	}

	extension := filepath.Ext(file.File)
	filename := strings.TrimSuffix(filepath.Base(file.File), extension)
	mime, err := mimetype.DetectFile(file.File)
	if err != nil {
		mime = &mimetype.MIME{}
		logctx.From(ctx).Error("detect file mime", zap.Error(err))
	}

	return Env{
		FilePath:  file.File,
		FileName:  filename,
		FileExt:   extension,
		ThumbPath: file.Thumb,
		MIME:      mime.String(),
	}
}
