package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// object is a JSON object that remembers the order of its keys.
//
// settings.json belongs to the user and often lives in a dotfiles repository: editing it must not
// reorder it, which a Go map would.
type object struct {
	keys   []string
	values map[string]any
}

func newObject() *object { return &object{values: map[string]any{}} }

func (o *object) get(key string) (any, bool) {
	value, ok := o.values[key]
	return value, ok
}

func (o *object) set(key string, value any) {
	if _, known := o.values[key]; !known {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *object) delete(key string) {
	if _, known := o.values[key]; !known {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// parseJSON decodes a document into objects, []any, strings, json.Number, bool and nil.
func parseJSON(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber() // a number is written back exactly as it was read
	value, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected text after the document")
	}
	return value, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		obj := newObject()
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			value, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			obj.set(key.(string), value)
		}
		_, err := dec.Token() // the closing brace
		return obj, err
	case '[':
		list := []any{}
		for dec.More() {
			value, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
		_, err := dec.Token() // the closing bracket
		return list, err
	}
	return nil, fmt.Errorf("unexpected %v", delim)
}

// formatJSON writes a document with one item per line, each level indented by indent.
func formatJSON(value any, indent string) string {
	var out bytes.Buffer
	writeValue(&out, value, indent, 0)
	return out.String()
}

func writeValue(out *bytes.Buffer, value any, indent string, depth int) {
	newline := func(depth int) {
		out.WriteByte('\n')
		out.WriteString(strings.Repeat(indent, depth))
	}
	switch v := value.(type) {
	case *object:
		if len(v.keys) == 0 {
			out.WriteString("{}")
			return
		}
		out.WriteByte('{')
		for i, key := range v.keys {
			if i > 0 {
				out.WriteByte(',')
			}
			newline(depth + 1)
			writeScalar(out, key)
			out.WriteString(": ")
			writeValue(out, v.values[key], indent, depth+1)
		}
		newline(depth)
		out.WriteByte('}')
	case []any:
		if len(v) == 0 {
			out.WriteString("[]")
			return
		}
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			newline(depth + 1)
			writeValue(out, item, indent, depth+1)
		}
		newline(depth)
		out.WriteByte(']')
	default:
		writeScalar(out, v)
	}
}

func writeScalar(out *bytes.Buffer, value any) {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false) // a command may hold & or >: keep it readable
	_ = enc.Encode(value)
	out.Truncate(out.Len() - 1) // Encode ends with a newline
}
