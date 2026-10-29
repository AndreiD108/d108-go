package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

func cutParts(t *testing.T, v any) (head, tail string, n int) {
	t.Helper()
	s, _ := v.(string)
	head, rest, ok := strings.Cut(s, "…[")
	if !ok {
		t.Fatalf("no sentinel in %.60q", s)
	}
	count, tail, ok := strings.Cut(rest, " bytes cut]…")
	if !ok {
		t.Fatalf("malformed sentinel in %.60q", rest)
	}
	if _, err := json.Number(count).Int64(); err != nil {
		t.Fatalf("count %q", count)
	}
	v64, _ := json.Number(count).Int64()
	return head, tail, int(v64)
}

func TestTruncateCutsLongestFirstAndFits(t *testing.T) {
	log, buf := capture(t)
	big := strings.Repeat("b", 70000)
	mid := strings.Repeat("m", 1000)
	log.Info("dump", "mid", mid, "big", big)
	raw := buf.Bytes()
	if len(raw) > MaxLineBytes {
		t.Errorf("line is %d bytes", len(raw))
	}
	got := one(t, buf)
	if got["mid"] != mid {
		t.Error("the shorter value must stay whole")
	}
	head, tail, n := cutParts(t, got["big"])
	if !strings.HasPrefix(big, head) || !strings.HasSuffix(big, tail) || n != len(big)-len(head)-len(tail) {
		t.Errorf("cut is inconsistent: head %d, tail %d, n %d", len(head), len(tail), n)
	}
	if len(head)-len(tail) > 1 || len(tail)-len(head) > 1 {
		t.Errorf("halves differ: %d and %d", len(head), len(tail))
	}
	if got["log.truncated"] != true {
		t.Error("log.truncated missing")
	}
}

func TestTruncateBodyAndTies(t *testing.T) {
	log, buf := capture(t)
	a := strings.Repeat("a", 40000)
	b := strings.Repeat("b", 40000)
	log.Info(a, "second", b)
	got := one(t, buf)
	if _, isCut := got["second"].(string); !isCut || strings.Contains(got["second"].(string), "…[") {
		t.Error("on a tie the key written first (body) is cut, not the later one")
	}
	head, _, _ := cutParts(t, got["body"])
	if !strings.HasPrefix(a, head) {
		t.Error("body was not cut from its own content")
	}
	if len(buf.Bytes()) > MaxLineBytes {
		t.Errorf("line is %d bytes", buf.Len())
	}
}

func TestTruncateEachValueOnceAndFloor(t *testing.T) {
	log, buf := capture(t)
	attrs := []any{}
	for i := 0; i < 300; i++ {
		attrs = append(attrs, "k"+string(rune('a'+i%26))+string(rune('a'+i/26)), strings.Repeat("v", 300))
	}
	log.Info("many", attrs...)
	got := one(t, buf)
	cuts := 0
	for k, v := range got {
		s, ok := v.(string)
		if !ok || !strings.Contains(s, "…[") {
			continue
		}
		cuts++
		if len(s) < MinKeptBytes {
			t.Errorf("%s kept %d bytes, under %d", k, len(s), MinKeptBytes)
		}
		if strings.Count(s, "…[") != 1 {
			t.Errorf("%s was cut more than once", k)
		}
	}
	if cuts == 0 {
		t.Error("no value was cut")
	}
	if got["log.truncated"] != true {
		t.Error("log.truncated missing")
	}
}

func TestTruncateKeepsUTF8Boundaries(t *testing.T) {
	log, buf := capture(t)
	s := strings.Repeat("é", 40000)
	log.Info("utf8", "s", s)
	raw := buf.Bytes()
	if !utf8.Valid(raw) {
		t.Fatal("line is not valid UTF-8")
	}
	got := one(t, buf)
	head, tail, n := cutParts(t, got["s"])
	if !utf8.ValidString(head) || !utf8.ValidString(tail) || n%2 != 0 {
		t.Errorf("cut fell inside a character: head %d, tail %d, n %d", len(head), len(tail), n)
	}
}

func TestLineThatCannotFitIsWrittenAsIs(t *testing.T) {
	log, buf := capture(t)
	var attrs []slog.Attr
	for i := 0; i < 400; i++ {
		attrs = append(attrs, slog.String("key_"+strings.Repeat("x", 10)+"_"+string(rune('a'+i%26))+string(rune('a'+i/26)), strings.Repeat("v", 200)))
	}
	log.LogAttrs(context.Background(), slog.LevelInfo, "wide", attrs...)
	if buf.Len() <= MaxLineBytes {
		t.Fatalf("test needs a line over the limit, got %d", buf.Len())
	}
	got := one(t, buf)
	if _, ok := got["log.truncated"]; ok {
		t.Error("nothing was cut, so log.truncated must be absent")
	}
	if len(got) != 400+8 {
		t.Errorf("keys were removed: %d", len(got))
	}
}

func TestTruncateSentinelDigitsCountAgainstTheLimit(t *testing.T) {
	// The sentinel's digit count grows with the bytes removed; the 8-process test once produced
	// a 65537-byte line this way. Try excesses around each power of ten.
	for _, extra := range []int{1, 9, 10, 99, 100, 999, 1000, 9979, 9980, 9981, 9999, 10000, 99979, 99980, 99999, 100000} {
		log, buf := capture(t)
		log.Info("edge", "pad", strings.Repeat("p", MaxLineBytes+extra))
		if buf.Len() > MaxLineBytes {
			t.Errorf("extra %d: line is %d bytes", extra, buf.Len())
		}
		got := one(t, buf)
		head, tail, n := cutParts(t, got["pad"])
		if n != MaxLineBytes+extra-len(head)-len(tail) {
			t.Errorf("extra %d: N = %d, want %d", extra, n, MaxLineBytes+extra-len(head)-len(tail))
		}
	}
}
