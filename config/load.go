// Package config loads an app's settings (standard.md section 9.5): a struct filled from
// `<Config>/config.json` by its json tags, then from the environment by its env tags, with
// `go-envconfig` as the one parser for both. A setting is a field with both tags; a nested
// struct is a group (`json:"sync" env:", prefix=SYNC_"`) and nests in the file. The file's
// values are handed to the parser as the strings their variables would hold: `8`, `true`,
// `"15m"` and `["a", "b"]` are `WORKERS=8`, `DRY_RUN=true`, `INTERVAL=15m` and `LIST=a,b`.
// So a list item cannot contain the delimiter in either source, and a file value for a
// scalar is a string, a number or a boolean.
//
// Precedence per setting: the variable when it is present, else the file when it has the
// key, else the field's value before Load when it is not zero, else `default=` from the
// tag. An explicit zero, false or empty value in the file counts as set. A `required` setting that neither source
// gives, a value that does not parse, a key in the file that is no setting, and a file that
// is not a JSON object are usage failures naming the file key or the variable.
//
// Load registers the doctor section "settings": every setting with its effective value and
// source (`file`, `env NAME`, `default`); a field tagged `secret:"true"` shows `set` or
// `unset`. Flags are the app's: it applies them after Load.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/paths"
	"github.com/sethvargo/go-envconfig"
)

// FileName is the settings file inside the Config directory.
const FileName = "config.json"

// Option changes how Load reads.
type Option func(*loader)

// Prefix sets the variable prefix (with its trailing underscore). Load uses the identity's
// EnvPrefix when the option is absent. Prefix("") turns the prefix off, for a struct of
// foreign names loaded on its own.
func Prefix(prefix string) Option {
	return func(l *loader) { l.prefix = &prefix }
}

type loader struct {
	prefix *string
}

// Load fills cfg, a pointer to a struct, from the file and the environment and registers
// the doctor section. It panics on a struct the loader cannot use (a tag it does not know,
// a setting with one tag only), because that is the app's code.
func Load(ctx context.Context, id identity.Identity, cfg any, opts ...Option) error {
	var l loader
	for _, opt := range opts {
		opt(&l)
	}
	prefix := id.EnvPrefix()
	if l.prefix != nil {
		prefix = *l.prefix
	}
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf("config.Load: cfg must be a pointer to a struct, got %T", cfg))
	}
	fs := fields(v.Elem().Type(), prefix)
	path := filepath.Join(paths.For(id.Name, paths.Prefix(id.EnvPrefix())).Config, FileName)
	fileValues, err := readFile(path, fs)
	if err != nil {
		return err
	}
	lk := &lookup{byName: map[string]*field{}, file: fileValues, getenv: os.LookupEnv}
	for _, f := range fs {
		lk.byName[f.name] = f
		// The parser leaves a field alone when the value it found is empty. A setting a
		// source gives starts from zero, so an empty value clears what the app set before.
		if _, given := lk.Lookup(f.name); given {
			target := settable(v.Elem(), f.index)
			target.Set(reflect.Zero(target.Type()))
		}
	}
	lk.last = nil
	err = envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:           cfg,
		Lookuper:         envconfig.PrefixLookuper(prefix, lk),
		DefaultOverwrite: true,
	})
	if err != nil {
		return classify(err, lk.last, path)
	}
	registerDoctor(v.Elem(), fs)
	return nil
}

// classify turns the parser's error into a usage failure that names the setting, the
// source of its value and the value. A default that does not parse is the app's bug.
func classify(err error, f *field, path string) error {
	if f == nil {
		return failure.Wrap(err, failure.Internal, "settings cannot be parsed: "+err.Error(),
			"report this to the tool's author")
	}
	if errors.Is(err, envconfig.ErrMissingRequired) {
		return failure.Wrap(err, failure.Usage, "setting "+f.key+" is required",
			fmt.Sprintf("set key %s in %s or the variable %s", f.key, path, f.name),
			"key", f.key, "variable", f.name, "file", path)
	}
	cause := rootCause(err).Error()
	switch f.source {
	case sourceEnv:
		value, _ := os.LookupEnv(f.name)
		return failure.Wrap(err, failure.Usage,
			fmt.Sprintf("variable %s has a bad value %q: %s", f.name, value, cause),
			"fix the variable "+f.name, "key", f.key, "variable", f.name, "value", value)
	case sourceFile:
		return failure.Wrap(err, failure.Usage,
			fmt.Sprintf("key %s in %s has a bad value: %s", f.key, path, cause),
			"fix key "+f.key+" in "+path, "key", f.key, "variable", f.name, "file", path)
	}
	return failure.Wrap(err, failure.Internal,
		fmt.Sprintf("the default of setting %s does not parse: %s", f.key, cause),
		"report this to the tool's author", "key", f.key, "variable", f.name)
}

// rootCause is the innermost error: the parser wraps each level with the field's name.
func rootCause(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}
