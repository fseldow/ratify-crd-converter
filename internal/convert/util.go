package convert

import (
	"encoding/json"
	"fmt"
)

// resourceID builds a stable identifier for diagnostics, e.g. "Store/ns/name".
func resourceID(kind, namespace, name string) string {
	if namespace == "" {
		return fmt.Sprintf("%s/%s", kind, name)
	}
	return fmt.Sprintf("%s/%s/%s", kind, namespace, name)
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// scopeGroup returns the group key used to bucket resources into executors.
// Empty namespace means cluster-scoped.
func scopeGroup(namespace string) string {
	if namespace == "" {
		return "__cluster__"
	}
	return namespace
}
