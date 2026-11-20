package octoreview

import "encoding/json"

// Nullable is a request field with three states: unset (the zero value, omitted from the JSON
// body), explicitly null (clears an override, for example an entity's max_images), or a value.
// Build one with [Value] or [Null].
type Nullable[T any] struct {
	set   bool
	null  bool
	value T
}

// Value sets a field to v.
func Value[T any](v T) Nullable[T] { return Nullable[T]{set: true, value: v} }

// Null sets a field to JSON null, which clears an override and inherits the default.
func Null[T any]() Nullable[T] { return Nullable[T]{set: true, null: true} }

// IsZero reports an unset field; `omitzero` drops it from the body.
func (n Nullable[T]) IsZero() bool { return !n.set }

// Get returns the value and whether the field holds one (false when unset or null).
func (n Nullable[T]) Get() (T, bool) { return n.value, n.set && !n.null }

// MarshalJSON writes null or the value.
func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if n.null || !n.set {
		return []byte("null"), nil
	}
	return json.Marshal(n.value)
}

// UnmarshalJSON reads null or a value.
func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	n.set = true
	if string(data) == "null" {
		n.null = true
		return nil
	}
	n.null = false
	return json.Unmarshal(data, &n.value)
}
