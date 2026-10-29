package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/AndreiD108/d108-go/failure"
)

// readFile reads config.json and returns every setting it gives as the string the parser
// takes: the value a variable would hold. A missing file gives an empty map. A file that is
// not a JSON object, a key that is no setting, or a value of the wrong shape is a usage
// failure naming the file and the key.
func readFile(path string, fields []*field) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, failure.Wrap(err, failure.Usage, "cannot read "+path+": "+err.Error(),
			"fix the file's permissions or remove it", "file", path)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		return nil, failure.Wrap(err, failure.Usage, path+" is not a JSON object: "+err.Error(),
			"fix the file", "file", path)
	}
	if top == nil {
		return nil, failure.New(failure.Usage, path+" is not a JSON object: it is null",
			"fix the file", "file", path)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, failure.New(failure.Usage, path+" has content after the JSON object",
			"fix the file", "file", path)
	}
	values := map[string]string{}
	known := map[string]bool{}
	for _, f := range fields {
		for i := 1; i < len(f.path); i++ {
			known[strings.Join(f.path[:i], ".")] = false
		}
		known[f.key] = true
		v, ok := lookupPath(top, f.path)
		if !ok || v == nil {
			continue
		}
		s, err := toString(v, f)
		if err != nil {
			return nil, failure.New(failure.Usage,
				fmt.Sprintf("key %s in %s: %v", f.key, path, err),
				"fix key "+f.key+" in "+path, "file", path, "key", f.key)
		}
		values[f.name] = s
	}
	if bad := badKeys(top, nil, known); len(bad) > 0 {
		return nil, failure.New(failure.Usage,
			fmt.Sprintf("%s: %s", path, strings.Join(bad, "; ")),
			"fix the keys in the file", "file", path)
	}
	return values, nil
}

// lookupPath walks nested objects. A non-object on the way is "not set"; readFile reports
// it through unknownKeys when the path above it is a setting group.
func lookupPath(top map[string]any, path []string) (any, bool) {
	var cur any = top
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// badKeys lists, sorted, the dotted keys in the file that name no setting and the group keys
// whose value is not an object. known maps a key to true for a setting, false for a group.
func badKeys(obj map[string]any, path []string, known map[string]bool) []string {
	var out []string
	for k, v := range obj {
		p := append(append([]string{}, path...), k)
		key := strings.Join(p, ".")
		leaf, ok := known[key]
		switch {
		case !ok:
			out = append(out, "key "+key+" is no setting")
		case leaf:
		case v == nil:
		default:
			sub, ok := v.(map[string]any)
			if !ok {
				out = append(out, "key "+key+" must be an object")
				continue
			}
			out = append(out, badKeys(sub, p, known)...)
		}
	}
	sort.Strings(out)
	return out
}

// toString renders a file value as the parser's input: scalars as their JSON text, lists
// joined with the field's delimiter, objects as key:value items for map fields. Nested
// lists and objects inside a list, and objects for a scalar field, are errors.
func toString(v any, f *field) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case []any:
		items := make([]string, 0, len(x))
		for _, it := range x {
			s, err := scalar(it)
			if err != nil {
				return "", fmt.Errorf("list item %w", err)
			}
			if strings.Contains(s, f.delimiter) {
				return "", fmt.Errorf("list item %q contains the delimiter %q", s, f.delimiter)
			}
			items = append(items, s)
		}
		return strings.Join(items, f.delimiter), nil
	case map[string]any:
		if kind(f.typ) != reflect.Map {
			return "", errors.New("expected a value, got an object")
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		items := make([]string, 0, len(x))
		for _, k := range keys {
			s, err := scalar(x[k])
			if err != nil {
				return "", fmt.Errorf("item %s: %w", k, err)
			}
			if strings.Contains(k, f.delimiter) || strings.Contains(k, f.separator) ||
				strings.Contains(s, f.delimiter) || strings.Contains(s, f.separator) {
				return "", fmt.Errorf("item %s contains the delimiter %q or the separator %q", k, f.delimiter, f.separator)
			}
			items = append(items, k+f.separator+s)
		}
		return strings.Join(items, f.delimiter), nil
	}
	return "", fmt.Errorf("unexpected value %v", v)
}

func scalar(v any) (string, error) {
	switch v.(type) {
	case string, json.Number, bool:
		return toString(v, nil)
	}
	return "", errors.New("must be a string, number or boolean")
}

func kind(t reflect.Type) reflect.Kind {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind()
}
