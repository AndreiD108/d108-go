package work

import (
	"context"
	"testing"
	"time"
)

func TestProgressEveryTwentieth(t *testing.T) {
	log, buf := newLog(t)
	p := NewProgress(context.Background(), log, "reindex", 100)
	for range 100 {
		p.Done(1)
	}
	all := lines(t, buf)
	if len(all) != 20 {
		t.Fatalf("got %d lines, want 20", len(all))
	}
	want(t, all[0], "event_name", "job.progress")
	want(t, all[0], "job.name", "reindex")
	want(t, all[0], "job.id", p.ID())
	want(t, all[0], "job.items_done", "5")
	want(t, all[0], "job.items_total", "100")
	want(t, all[19], "job.items_done", "100")
	if len(p.ID()) != 26 {
		t.Errorf("job.id %q is not a ULID", p.ID())
	}
}

func TestProgressLastItemAlwaysWritten(t *testing.T) {
	log, buf := newLog(t)
	p := NewProgress(context.Background(), log, "x", 7)
	p.Done(3)
	p.Done(4)
	all := lines(t, buf)
	if len(all) != 2 {
		t.Fatalf("got %d lines, want 2: %s", len(all), buf.String())
	}
	want(t, all[1], "job.items_done", "7")
}

func TestProgressSmallTotalEveryItem(t *testing.T) {
	log, buf := newLog(t)
	p := NewProgress(context.Background(), log, "x", 3)
	p.Done(1)
	p.Done(1)
	p.Done(1)
	if n := len(lines(t, buf)); n != 3 {
		t.Errorf("got %d lines, want 3", n)
	}
}

func TestProgressUnknownTotalByTimeOnly(t *testing.T) {
	log, buf := newLog(t)
	p := NewProgress(context.Background(), log, "x", 0)
	now := time.Now()
	p.now = func() time.Time { return now }
	for range 50 {
		p.Done(1)
	}
	if n := len(lines(t, buf)); n != 0 {
		t.Fatalf("wrote %d lines before the interval", n)
	}
	now = now.Add(ProgressInterval)
	p.Done(1)
	all := lines(t, buf)
	if len(all) != 1 {
		t.Fatalf("got %d lines, want 1", len(all))
	}
	want(t, all[0], "job.items_done", "51")
	absent(t, all[0], "job.items_total")
}

func TestProgressIntervalWithTotal(t *testing.T) {
	log, buf := newLog(t)
	p := NewProgress(context.Background(), log, "x", 1000)
	now := time.Now()
	p.now = func() time.Time { return now }
	p.Done(1)
	now = now.Add(ProgressInterval)
	p.Done(1)
	all := lines(t, buf)
	if len(all) != 1 {
		t.Fatalf("got %d lines, want 1", len(all))
	}
	want(t, all[0], "job.items_done", "2")
}
