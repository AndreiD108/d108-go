package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/AndreiD108/d108-go/doctor"
)

// registerDoctor snapshots the settings after Load: the struct's values are read once, so
// the report does not race with the app's later writes to the struct.
func registerDoctor(v reflect.Value, fs []*field) {
	items := make([]doctor.Item, 0, len(fs))
	for _, f := range fs {
		items = append(items, doctor.Item{Key: f.key, Value: render(fieldValue(v, f.index), f.secret), Source: f.sourceText()})
	}
	doctor.Add("settings", func() []doctor.Item { return items })
}

func (f *field) sourceText() string {
	switch f.source {
	case sourceEnv:
		return "env " + f.name
	case sourceFile:
		return "file"
	}
	return "default"
}

// settable follows the index path to the field and allocates the nil pointers on the way,
// so the result can be set.
func settable(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}

// fieldValue follows the index path through pointers; a nil pointer on the way gives the
// zero value of the field's type.
func fieldValue(v reflect.Value, index []int) reflect.Value {
	for n, i := range index {
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Zero(v.Type().Elem().FieldByIndex(index[n:]).Type)
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}

// render prints a value for the report: a secret as set or unset, a list as its items
// joined with ", ", a map as sorted key=value items, the rest with %v (a Duration prints
// as "15m0s").
func render(v reflect.Value, secret bool) string {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = reflect.Zero(v.Type().Elem())
		} else {
			v = v.Elem()
		}
	}
	if secret {
		if v.IsZero() {
			return "unset"
		}
		return "set"
	}
	switch v.Kind() {
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return string(v.Bytes())
		}
		items := make([]string, v.Len())
		for i := range items {
			items[i] = render(v.Index(i), false)
		}
		return strings.Join(items, ", ")
	case reflect.Map:
		items := make([]string, 0, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			items = append(items, render(iter.Key(), false)+"="+render(iter.Value(), false))
		}
		sort.Strings(items)
		return strings.Join(items, ", ")
	}
	return fmt.Sprint(v.Interface())
}
