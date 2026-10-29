package work

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/AndreiD108/d108-go/logging"
)

// ProgressInterval is the longest silence between two job.progress lines while Done is called.
const ProgressInterval = 10 * time.Second

// Progress writes job.progress for one job run: every N items (N is total/20, at least 1), when
// ProgressInterval has passed since the last line, and when the count reaches total. With a
// total of 0 (unknown) only the interval rule applies and job.items_total is omitted.
type Progress struct {
	ctx   context.Context
	log   *slog.Logger
	name  string
	id    string
	total int
	every int
	now   func() time.Time

	mu       sync.Mutex
	done     int
	lastLine int
	lastAt   time.Time
}

// NewProgress starts a job run named jobName with total items (0 when unknown). job.id is a ULID
// for this run. A nil log uses the logger in ctx.
func NewProgress(ctx context.Context, log *slog.Logger, jobName string, total int) *Progress {
	if log == nil {
		log = logging.FromContext(ctx)
	}
	every := total / 20
	if every < 1 {
		every = 1
	}
	return &Progress{
		ctx: ctx, log: log, name: jobName, id: ulid.Make().String(),
		total: total, every: every, now: time.Now, lastAt: time.Now(),
	}
}

// Done adds n finished items and writes job.progress when a rule says so. Safe for concurrent use.
func (p *Progress) Done(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
	now := p.now()
	switch {
	case p.total > 0 && p.done >= p.total && p.lastLine < p.total:
	case p.total > 0 && p.done/p.every > p.lastLine/p.every:
	case now.Sub(p.lastAt) >= ProgressInterval:
	default:
		return
	}
	p.lastLine, p.lastAt = p.done, now
	attrs := []slog.Attr{
		logging.Event("job.progress"),
		slog.String("job.name", p.name),
		slog.String("job.id", p.id),
		slog.Int("job.items_done", p.done),
	}
	if p.total > 0 {
		attrs = append(attrs, slog.Int("job.items_total", p.total))
	}
	p.log.LogAttrs(p.ctx, slog.LevelInfo, "", attrs...)
}

// ID returns the job.id of this run, for the app's own lines.
func (p *Progress) ID() string { return p.id }
