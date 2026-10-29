package logging

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// maxDepth bounds the walk of nested values, so a cyclic structure ends as a string.
const maxDepth = 32

// jsonNumber is the number grammar of RFC 8259; a json.Number outside it is written as a string.
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// plainValue turns any Go value into JSON-shaped data without encoding/json's failures: a
// map[string]any for objects (maps and structs, by json tags), []any for arrays, string,
// int64, uint64, float64, bool or json.Number for scalars, nil for null. Non-finite floats
// become the strings NaN, +Inf and -Inf wherever they sit, so one bad number never loses its
// siblings. Types with their own JSON or text form (time.Time, json.Marshaler,
// encoding.TextMarshaler) keep it.
func plainValue(v reflect.Value, depth int) any {
	if !v.IsValid() {
		return nil
	}
	if depth > maxDepth {
		return fmt.Sprint(v.Interface())
	}
	if v.CanInterface() {
		switch x := v.Interface().(type) {
		case json.Number:
			if jsonNumber.MatchString(string(x)) {
				return x
			}
			return string(x)
		case time.Time:
			return x.UTC().Format(time.RFC3339Nano)
		case time.Duration:
			return x.String()
		case []byte:
			return string(x)
		case error:
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return nil
			}
			return x.Error()
		case json.Marshaler:
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return nil
			}
			return viaJSON(x)
		case encoding.TextMarshaler:
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return nil
			}
			b, err := x.MarshalText()
			if err != nil {
				return "!" + err.Error()
			}
			return string(b)
		case fmt.Stringer:
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return nil
			}
			if v.Kind() != reflect.Struct && v.Kind() != reflect.Pointer {
				return x.String()
			}
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return plainValue(v.Elem(), depth+1)
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint()
	case reflect.Float32, reflect.Float64:
		return finite(v.Float())
	case reflect.String:
		return v.String()
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = plainValue(v.Index(i), depth+1)
		}
		return out
	case reflect.Map:
		if v.IsNil() || v.Len() == 0 {
			return nil
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[mapKey(iter.Key())] = plainValue(iter.Value(), depth+1)
		}
		return out
	case reflect.Struct:
		out := make(map[string]any)
		structFields(v, out, depth)
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return fmt.Sprint(v.Interface())
}

// finite keeps a float JSON can carry and names the others.
func finite(f float64) any {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	return f
}

func mapKey(k reflect.Value) string {
	if k.Kind() == reflect.String {
		return k.String()
	}
	if k.CanInterface() {
		if tm, ok := k.Interface().(encoding.TextMarshaler); ok {
			if b, err := tm.MarshalText(); err == nil {
				return string(b)
			}
		}
	}
	return fmt.Sprint(k.Interface())
}

// structFields adds the exported fields of v to out by their json tags: "-" skips, omitempty
// skips a zero value, an untagged embedded struct is inlined.
func structFields(v reflect.Value, out map[string]any, depth int) {
	if depth > maxDepth {
		return
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		tag, hasTag := f.Tag.Lookup("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" && !strings.Contains(tag, ",") {
			continue
		}
		fv := v.Field(i)
		if f.Anonymous && !hasTag {
			for fv.Kind() == reflect.Pointer {
				if fv.IsNil() {
					break
				}
				fv = fv.Elem()
			}
			if fv.Kind() == reflect.Struct {
				structFields(fv, out, depth+1)
				continue
			}
			if !f.IsExported() {
				continue
			}
		}
		if strings.Contains(opts, "omitempty") && fv.IsZero() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = plainValue(fv, depth+1)
	}
}

// viaJSON keeps the JSON form a json.Marshaler gives, decoded with numbers as json.Number.
func viaJSON(m json.Marshaler) any {
	b, err := m.MarshalJSON()
	if err != nil {
		return "!" + err.Error()
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(b)
	}
	return v
}
