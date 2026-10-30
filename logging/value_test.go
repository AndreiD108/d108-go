package logging

import (
	"encoding/json"
	"log/slog"
	"math"
	"testing"

	"github.com/AndreiD108/d108-go/contracttest"
)

func TestNestedNonFiniteKeepsSiblings(t *testing.T) {
	log, buf := capture(t)
	type stats struct {
		Mean float64   `json:"mean"`
		Max  float64   `json:"max"`
		Hist []float64 `json:"hist"`
		Skip string    `json:"-"`
		Opt  string    `json:"opt,omitempty"`
	}
	log.Info("s", "st", stats{Mean: math.NaN(), Max: 3, Hist: []float64{1, math.Inf(1)}, Skip: "x"},
		"m", map[string]any{"a": math.Inf(-1), "b": "ok"})
	got := one(t, buf)
	want := map[string]any{
		"st.mean": "NaN", "st.max": json.Number("3"), "st.hist": `[1,"+Inf"]`,
		"m.a": "-Inf", "m.b": "ok",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
	for _, k := range []string{"st.skip", "st.opt", "st.Skip"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must not be written", k)
		}
	}
}

func TestInlineGroupUnderWithGroup(t *testing.T) {
	log, buf := capture(t)
	log.WithGroup("db").With(slog.Group("", slog.Int("n", 1))).WithGroup("q").Info("x", slog.Group("", slog.String("s", "v")), "k", 2)
	raw := append([]byte(nil), buf.Bytes()...)
	t.Run("contract", func(t *testing.T) { contracttest.Check(t, [][]byte{raw}) })
	got := one(t, buf)
	if got["db.n"] != json.Number("1") || got["db.q.s"] != "v" || got["db.q.k"] != json.Number("2") {
		t.Errorf("got %v", got)
	}
}

func TestMalformedNumberBecomesString(t *testing.T) {
	log, buf := capture(t)
	log.Info("n", "bad", json.Number("1.2.3"), "nan", json.Number("NaN"), "inf", json.Number("+Inf"),
		"plus", json.Number("+1"), "lead", json.Number("01"), "hex", json.Number("0x1"),
		"good", json.Number("42"), "neg", json.Number("-1.5e-3"), "sib", "kept")
	raw := append([]byte(nil), buf.Bytes()...)
	t.Run("contract", func(t *testing.T) { contracttest.Check(t, [][]byte{raw}) })
	got := one(t, buf)
	want := map[string]any{
		"bad": "1.2.3", "nan": "NaN", "inf": "+Inf", "plus": "+1", "lead": "01", "hex": "0x1",
		"good": json.Number("42"), "neg": json.Number("-1.5e-3"), "sib": "kept",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
}

type embedded struct {
	Inner int `json:"inner"`
}

type outer struct {
	embedded
	Name string
	Ptr  *int
	When map[string]int `json:"when"`
}

func TestStructShapes(t *testing.T) {
	log, buf := capture(t)
	log.Info("o", "o", outer{embedded: embedded{Inner: 7}, Name: "n", When: map[string]int{"b": 2, "a": 1}})
	got := one(t, buf)
	if got["o.inner"] != json.Number("7") || got["o.Name"] != "n" || got["o.when.a"] != json.Number("1") || got["o.when.b"] != json.Number("2") {
		t.Errorf("got %v", got)
	}
	if _, ok := got["o.Ptr"]; ok {
		t.Error("a nil pointer is null and must be left out")
	}
}

type node struct {
	*node
	V int `json:"v"`
}

func TestCyclicEmbeddedStructEnds(t *testing.T) {
	log, buf := capture(t)
	n := &node{V: 1}
	n.node = n
	log.Info("cycle", "n", n, "after", "kept")
	raw := append([]byte(nil), buf.Bytes()...)
	t.Run("contract", func(t *testing.T) { contracttest.Check(t, [][]byte{raw}) })
	got := one(t, buf)
	if got["after"] != "kept" || got["n.v"] != json.Number("1") {
		t.Errorf("got %v", got)
	}
}
