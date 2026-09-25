package guard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// parseStrict decodes a JSON object and refuses what could make the guard
// and Google read different values: a key given twice, or one field given in
// both spellings Google accepts (amountMicros and amount_micros). Keys come
// back in lowerCamelCase and numbers as json.Number.
func parseStrict(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec, "")
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the JSON body")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("the body must be a JSON object")
	}
	return obj, nil
}

func parseValue(dec *json.Decoder, path string) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil // string, json.Number, bool or nil
	}
	switch delim {
	case '{':
		obj := map[string]any{}
		raw := map[string]string{} // camelCase key -> key as written
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := kt.(string)
			camel := toCamel(key)
			if prev, dup := raw[camel]; dup {
				if prev == key {
					return nil, fmt.Errorf("duplicate key %q at %s", key, where(path))
				}
				return nil, fmt.Errorf("field %q given in both spellings (%q and %q) at %s", camel, prev, key, where(path))
			}
			raw[camel] = key
			v, err := parseValue(dec, join(path, camel))
			if err != nil {
				return nil, err
			}
			obj[camel] = v
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return obj, nil
	case '[':
		arr := []any{}
		for i := 0; dec.More(); i++ {
			v, err := parseValue(dec, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, fmt.Errorf("unexpected %v at %s", delim, where(path))
}

// toCamel converts snake_case to lowerCamelCase; other names pass through.
func toCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	parts := strings.Split(s, "_")
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if p != "" {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func where(path string) string {
	if path == "" {
		return "the top level"
	}
	return path
}
