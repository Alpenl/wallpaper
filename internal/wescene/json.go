package wescene

import (
	"encoding/json"
	"fmt"
	"io"
)

const MaxNodes = 100000
const MaxDepth = 64

// decodeJSON rejects duplicate keys at every depth, including unknown fields.
// Tokens (keys and containers included) consume the node budget before allocation.
func decodeJSON(r io.Reader) (any, error) {
	d := json.NewDecoder(r)
	d.UseNumber()
	left := MaxNodes
	v, err := readValue(d, 0, &left)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one JSON value")
	}
	return v, nil
}
func readValue(d *json.Decoder, depth int, left *int) (any, error) {
	if depth > MaxDepth || *left <= 0 {
		return nil, fmt.Errorf("JSON depth or node budget exceeded")
	}
	*left--
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		m := make(map[string]any)
		for d.More() {
			if *left <= 0 {
				return nil, fmt.Errorf("JSON node budget exceeded")
			}
			*left--
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			k, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid JSON key")
			}
			if _, exists := m[k]; exists {
				return nil, fmt.Errorf("duplicate JSON key")
			}
			v, err := readValue(d, depth+1, left)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		return m, nil
	case '[':
		a := make([]any, 0)
		for d.More() {
			v, err := readValue(d, depth+1, left)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		return a, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}
