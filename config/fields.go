package config

import (
	"encoding"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/sethvargo/go-envconfig"
)

// source says where a setting's effective value came from.
type source int

const (
	sourceDefault source = iota
	sourceFile
	sourceEnv
)

// field is one setting: a leaf of the struct with both tags.
type field struct {
	// key is the file key path, dotted: "sync.interval".
	key string
	// path is key split: the JSON object path in the file.
	path []string
	// name is the full variable name with every prefix: "SLACK_CLI_SYNC_INTERVAL".
	name string
	// index is the struct field index path for reflect.Value.FieldByIndex.
	index []int
	typ   reflect.Type
	// delimiter and separator split lists and map items, as go-envconfig does.
	delimiter, separator string
	hasDefault           bool
	required             bool
	secret               bool
	// Filled by Load.
	source source
}

var namePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// fields lists the settings of a struct type, in declaration order. It panics on a tag the
// loader cannot use: the struct is the app's code, not input.
func fields(t reflect.Type, prefix string) []*field {
	var out []*field
	walk(t, prefix, nil, nil, false, &out)
	return out
}

// walk collects the settings of t. secret is true under a group tagged secret:"true": every
// setting below it is shown as set or unset.
func walk(t reflect.Type, prefix string, path []string, index []int, secret bool, out *[]*field) {
	for i := range t.NumField() {
		sf := t.Field(i)
		jsonKey, hasJSON := jsonName(sf)
		envTag, hasEnv := sf.Tag.Lookup("env")
		idx := append(append([]int{}, index...), i)
		where := t.String() + "." + sf.Name
		secret := secret || sf.Tag.Get("secret") == "true"
		if !sf.IsExported() {
			if hasJSON || hasEnv {
				panic(fmt.Sprintf("config: %s is not exported", where))
			}
			continue
		}
		elem := sf.Type
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		nested := elem.Kind() == reflect.Struct && !isDecoder(elem)
		if sf.Anonymous && !hasJSON && !hasEnv {
			if !nested {
				continue
			}
			walk(elem, prefix, path, idx, secret, out)
			continue
		}
		if !hasJSON && !hasEnv {
			continue
		}
		if !hasJSON || !hasEnv {
			panic(fmt.Sprintf("config: %s needs both a json and an env tag", where))
		}
		key, opts := parseEnvTag(envTag, where)
		fieldPath := append(append([]string{}, path...), jsonKey)
		if nested {
			if key != "" {
				panic(fmt.Sprintf("config: %s is a struct; its env tag names a prefix, not a variable", where))
			}
			walk(elem, prefix+opts.prefix, fieldPath, idx, secret, out)
			continue
		}
		if key == "" {
			panic(fmt.Sprintf("config: %s has no variable name in its env tag", where))
		}
		if opts.prefix != "" {
			panic(fmt.Sprintf("config: %s is not a struct; prefix= is for structs", where))
		}
		if opts.required && opts.hasDefault {
			panic(fmt.Sprintf("config: %s is required and has a default", where))
		}
		*out = append(*out, &field{
			key:        strings.Join(fieldPath, "."),
			path:       fieldPath,
			name:       prefix + key,
			index:      idx,
			typ:        sf.Type,
			delimiter:  opts.delimiter,
			separator:  opts.separator,
			hasDefault: opts.hasDefault,
			required:   opts.required,
			secret:     secret,
		})
	}
}

// jsonName is the key of a field in the file: the json tag's name. A "-" tag hides the field.
func jsonName(sf reflect.StructField) (string, bool) {
	tag, ok := sf.Tag.Lookup("json")
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return "", false
	}
	return name, true
}

type envOptions struct {
	prefix, delimiter, separator string
	hasDefault, required         bool
}

// parseEnvTag reads the go-envconfig tag: the variable name, then options. It accepts the
// options go-envconfig accepts and panics on the rest, with the same escaping rule for
// commas in a default.
func parseEnvTag(tag, where string) (string, envOptions) {
	opts := envOptions{delimiter: ",", separator: ":"}
	parts := splitEscaped(tag, ",", "\\")
	key := strings.TrimSpace(parts[0])
	if key != "" && !namePattern.MatchString(key) {
		panic(fmt.Sprintf("config: %s: %q is not a variable name", where, key))
	}
	for _, o := range parts[1:] {
		o = strings.TrimLeftFunc(o, func(r rune) bool { return r == ' ' || r == '\t' })
		word := strings.TrimSpace(o)
		lower := strings.ToLower(o)
		switch {
		case strings.EqualFold(word, "required"):
			opts.required = true
		case strings.EqualFold(word, "overwrite"), strings.EqualFold(word, "noinit"), strings.EqualFold(word, "decodeunset"):
		case strings.HasPrefix(lower, "prefix="):
			opts.prefix = o[len("prefix="):]
		case strings.HasPrefix(lower, "delimiter="):
			opts.delimiter = o[len("delimiter="):]
		case strings.HasPrefix(lower, "separator="):
			opts.separator = o[len("separator="):]
		case strings.HasPrefix(lower, "default="):
			opts.hasDefault = true
			return key, opts
		default:
			panic(fmt.Sprintf("config: %s: unknown env tag option %q", where, word))
		}
	}
	return key, opts
}

// splitEscaped splits s on sep unless the separator is escaped by esc.
func splitEscaped(s, sep, esc string) []string {
	a := strings.Split(s, sep)
	for i := len(a) - 2; i >= 0; i-- {
		if strings.HasSuffix(a[i], esc) {
			a[i] = a[i][:len(a[i])-len(esc)] + sep + a[i+1]
			a = append(a[:i+1], a[i+2:]...)
		}
	}
	return a
}

var decoderTypes = []reflect.Type{
	reflect.TypeFor[envconfig.DecoderCtx](),
	reflect.TypeFor[envconfig.Decoder](),
	reflect.TypeFor[encoding.TextUnmarshaler](),
	reflect.TypeFor[json.Unmarshaler](),
	reflect.TypeFor[encoding.BinaryUnmarshaler](),
	reflect.TypeFor[gob.GobDecoder](),
}

// isDecoder reports whether a struct type parses itself from a string (go-envconfig's
// decoder interfaces), so it is a leaf, not a nested struct.
func isDecoder(t reflect.Type) bool {
	p := reflect.PointerTo(t)
	for _, d := range decoderTypes {
		if p.Implements(d) {
			return true
		}
	}
	return false
}

// lookup answers go-envconfig: the environment first, then the file. It records the source
// of every setting it answers, the settings resolved so far and the last setting asked, so
// a parse error can be named and the doctor section lists what was resolved before it.
type lookup struct {
	byName map[string]*field
	file   map[string]string
	getenv func(string) (string, bool)
	// resolved holds the settings asked before last, in order: the parser sets each field
	// before it asks for the next, so these hold their values when last fails.
	resolved []*field
	last     *field
}

func (l *lookup) Lookup(name string) (string, bool) {
	f := l.byName[name]
	if f != nil && f != l.last {
		if l.last != nil {
			l.resolved = append(l.resolved, l.last)
		}
		l.last = f
	}
	if v, ok := l.getenv(name); ok {
		if f != nil {
			f.source = sourceEnv
		}
		return v, true
	}
	if v, ok := l.file[name]; ok {
		if f != nil {
			f.source = sourceFile
		}
		return v, true
	}
	return "", false
}
