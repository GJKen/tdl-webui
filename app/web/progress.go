package web

import "github.com/iyear/tdl/core/uploader"

// webProgress reports a single upload's progress into the task store as a
// percentage, so the browser can render a progress ring. Each web upload is one
// up.Run with one file, so the elem is ignored — the task id is fixed per run.
type webProgress struct {
	tasks *TaskStore
	id    string
}

func (p *webProgress) OnAdd(uploader.Elem) {}

func (p *webProgress) OnUpload(_ uploader.Elem, st uploader.ProgressState) {
	pct := 0.0
	if st.Total > 0 {
		pct = float64(st.Uploaded) / float64(st.Total) * 100
	}
	p.tasks.Update(p.id, func(t *Task) { t.Progress = pct })
}

func (p *webProgress) OnDone(_ uploader.Elem, err error) {
	if err == nil {
		return
	}
	// A per-file send failure (e.g. no permission in the target) is reported
	// here, not via up.Run's return value (which keeps a batch going). Record it
	// on the task so the bubble shows ⚠ instead of a false ✓, translating
	// permission errors to a clear "无权限发送".
	p.tasks.Update(p.id, func(t *Task) {
		t.Status = TaskError
		t.Error = sendErrorDisplay(err)
	})
}
