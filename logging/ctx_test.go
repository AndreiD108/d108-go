package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/AndreiD108/d108-go/identity"
)

func TestAttrSetAppearsOnLaterLinesAndNests(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	var buf bytes.Buffer
	log := New(identity.New("ctxtest"), Writer(&buf))

	ctx := WithAttrs(context.Background(), slog.String("fixed", "f"), slog.String("k", "fixed"))
	ctx, outer := WithAttrSet(ctx)
	outer.Add(slog.String("outer", "o"), slog.String("k", "outer"))
	inner, set := WithAttrSet(ctx)
	set.Add(slog.String("inner", "i"))

	log.InfoContext(inner, "a", "k", "record")
	set.Add(slog.String("late", "l"))
	log.InfoContext(inner, "b")
	log.InfoContext(ctx, "c")

	var got []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		got = append(got, m)
	}
	if len(got) != 3 {
		t.Fatalf("got %d lines", len(got))
	}
	a, b, c := got[0], got[1], got[2]
	if a["fixed"] != "f" || a["outer"] != "o" || a["inner"] != "i" || a["k"] != "record" {
		t.Errorf("line a: %v", a)
	}
	if _, ok := a["late"]; ok {
		t.Errorf("line a carries an attribute added after it: %v", a)
	}
	if b["late"] != "l" || b["k"] != "outer" {
		t.Errorf("line b: %v", b)
	}
	if _, ok := c["inner"]; ok || c["outer"] != "o" {
		t.Errorf("the outer context sees the inner set: %v", c)
	}
	if AttrSetFrom(context.Background()) != nil {
		t.Error("AttrSetFrom on a bare context is not nil")
	}
	var nilSet *AttrSet
	nilSet.Add(slog.String("x", "y"))
	if nilSet.Attrs() != nil {
		t.Error("a nil set has attributes")
	}
}
