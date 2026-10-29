package logging

import (
	"strconv"
	"unicode/utf8"
)

// MaxLineBytes is the longest line written, newline included (standard.md section 2.3).
const MaxLineBytes = 64 << 10

// MinKeptBytes is the shortest a string value is cut to.
const MinKeptBytes = 256

// truncate cuts string values until the serialized line fits MaxLineBytes, by the rules of
// standard.md section 2.3: the longest string value first (ties to the key written first),
// cut in the middle by the excess, to no less than MinKeptBytes, each value at most once.
// It returns the serialized line and whether anything was cut.
func (l *line) truncate() ([]byte, bool) {
	out := l.json()
	if len(out) <= MaxLineBytes {
		return out, false
	}
	l.set("log.truncated", true)
	cut := make(map[int]bool)
	for {
		out = l.json()
		excess := len(out) - MaxLineBytes
		if excess <= 0 {
			return out, true
		}
		i := l.longestString(cut)
		if i < 0 {
			break
		}
		cut[i] = true
		l.fields[i].val = cutMiddle(l.fields[i].val.(string), excess)
	}
	if len(cut) == 0 {
		l.unsetLast("log.truncated")
		return l.json(), false
	}
	return out, true
}

// longestString returns the index of the longest uncut string value above MinKeptBytes, or
// -1 when there is none.
func (l *line) longestString(cut map[int]bool) int {
	best, bestLen := -1, MinKeptBytes
	for i, f := range l.fields {
		s, ok := f.val.(string)
		if !ok || cut[i] || len(s) <= bestLen {
			continue
		}
		best, bestLen = i, len(s)
	}
	return best
}

// cutMiddle removes about excess bytes from the middle of s, plus room for the sentinel,
// keeping equal head and tail halves on UTF-8 boundaries and at least MinKeptBytes.
func cutMiddle(s string, excess int) string {
	n := len(s)
	// The sentinel's own bytes count against the excess; its digit count depends on the
	// total removed, so size it from a first estimate.
	sentinel := len(sentinelFor(excess + len(sentinelFor(excess))))
	keep := n - excess - sentinel
	if keep < MinKeptBytes {
		keep = MinKeptBytes
	}
	headLen := keep / 2
	for headLen > 0 && !utf8.RuneStart(s[headLen]) {
		headLen--
	}
	tailStart := n - (keep - headLen)
	for tailStart < n && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	if tailStart < headLen {
		tailStart = headLen
	}
	return s[:headLen] + sentinelFor(tailStart-headLen) + s[tailStart:]
}

func sentinelFor(removed int) string {
	return "…[" + strconv.Itoa(removed) + " bytes cut]…"
}
