package downloader

import (
	"context"
	"time"

	"go.uber.org/atomic"
	"golang.org/x/time/rate"
)

type Progress interface {
	OnAdd(elem Elem)
	OnDownload(elem Elem, state ProgressState)
	OnDone(elem Elem, err error)
	// TODO: OnLog to log something that is not an error but should be sent to the user
}

type ProgressState struct {
	Downloaded int64
	Total      int64
}

// writeAt wrapper for file to use progress bar
//
// do not need mutex because gotd has use syncio.WriteAt
type writeAt struct {
	elem     Elem
	progress Progress
	partSize int

	downloaded *atomic.Int64

	// ctx and limiter throttle writes when a global rate limit is set. WriteAt
	// is the single point every task's every part flows through, and rate.Limiter
	// is safe for concurrent use, so a shared limiter here bounds the total rate.
	// limiter is nil when unlimited.
	ctx     context.Context
	limiter *rate.Limiter
}

func newWriteAt(ctx context.Context, elem Elem, progress Progress, partSize int, limiter *rate.Limiter) *writeAt {
	return &writeAt{
		elem:       elem,
		progress:   progress,
		partSize:   partSize,
		downloaded: atomic.NewInt64(0),
		ctx:        ctx,
		limiter:    limiter,
	}
}

func (w *writeAt) WriteAt(p []byte, off int64) (int, error) {
	// Throttle before writing; len(p) <= partSize <= the limiter burst, so WaitN
	// blocks for the right duration rather than erroring on an oversized request.
	if w.limiter != nil {
		if err := w.limiter.WaitN(w.ctx, len(p)); err != nil {
			return 0, err
		}
	}

	at, err := w.elem.To().WriteAt(p, off)
	if err != nil {
		return 0, err
	}

	// some small files may finish too fast, terminal history may not be overwritten
	// this is just a simple way to avoid the problem
	if at < w.partSize { //  last part(every file only exec once)
		time.Sleep(time.Millisecond * 200) // to ensure the progress render next time
	}
	w.progress.OnDownload(w.elem, ProgressState{
		Downloaded: w.downloaded.Add(int64(at)),
		Total:      w.elem.File().Size(),
	})
	return at, nil
}
