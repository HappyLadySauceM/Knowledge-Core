package configcenter

import (
	"reflect"
	"sort"
	"strings"
	"time"
)

var durationType = reflect.TypeFor[time.Duration]()

func RestartRequiredFields(startup, candidate any, mutablePaths ...string) []string {
	mutable := make(map[string]struct{}, len(mutablePaths))
	for _, path := range mutablePaths {
		mutable[path] = struct{}{}
	}
	var changed []string
	collectChanged(reflect.ValueOf(startup), reflect.ValueOf(candidate), "", mutable, &changed)
	sort.Strings(changed)
	return changed
}

func collectChanged(left, right reflect.Value, path string, mutable map[string]struct{}, changed *[]string) {
	for left.IsValid() && (left.Kind() == reflect.Pointer || left.Kind() == reflect.Interface) {
		if left.IsNil() {
			break
		}
		left = left.Elem()
	}
	for right.IsValid() && (right.Kind() == reflect.Pointer || right.Kind() == reflect.Interface) {
		if right.IsNil() {
			break
		}
		right = right.Elem()
	}
	if !left.IsValid() || !right.IsValid() || left.Type() != right.Type() || left.Type() == durationType || left.Kind() != reflect.Struct {
		if path != "" && !reflect.DeepEqual(valueInterface(left), valueInterface(right)) {
			if _, ok := mutable[path]; !ok {
				*changed = append(*changed, path)
			}
		}
		return
	}
	for index := 0; index < left.NumField(); index++ {
		field := left.Type().Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("mapstructure"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		child := name
		if path != "" {
			child = path + "." + name
		}
		collectChanged(left.Field(index), right.Field(index), child, mutable, changed)
	}
}

func valueInterface(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return value.Interface()
}
