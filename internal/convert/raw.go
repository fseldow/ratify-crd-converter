package convert

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/runtime"
)

// rawToMap unmarshals a RawExtension into a generic map. Returns an empty,
// non-nil map when the extension is empty.
func rawToMap(raw runtime.RawExtension) (map[string]any, error) {
	m := map[string]any{}
	if len(raw.Raw) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(raw.Raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// mapToRaw marshals a map back into a RawExtension. An empty map yields a zero
// RawExtension so it is omitted from output.
func mapToRaw(m map[string]any) (runtime.RawExtension, error) {
	if len(m) == 0 {
		return runtime.RawExtension{}, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return runtime.RawExtension{}, err
	}
	return runtime.RawExtension{Raw: b}, nil
}

// valueToRaw marshals any value into a RawExtension.
func valueToRaw(v any) (runtime.RawExtension, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return runtime.RawExtension{}, err
	}
	return runtime.RawExtension{Raw: b}, nil
}
