// Package jsjson emits the closed JSON artifact form used by the corrected
// cross-runtime adapter proof. It is not a general canonical-JSON package.
package jsjson

import (
	"bytes"
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

const MaxExactInteger int64 = 9_007_199_254_740_991

var errUnsupported = errors.New("jsjson: unsupported value")

// Object is a JSON object. Member order is ignored and emitted in JavaScript
// UTF-16 key order.
type Object map[string]any

// Encode emits two-space-indented bytes with one trailing newline. The only
// admitted values are Object/map[string]any, []any, string, and exact integers.
func Encode(value any) ([]byte, error) {
	var output bytes.Buffer
	if err := appendValue(&output, value, 0); err != nil {
		return nil, errUnsupported
	}
	output.WriteByte('\n')
	return output.Bytes(), nil
}

func appendValue(output *bytes.Buffer, value any, depth int) error {
	switch typed := value.(type) {
	case Object:
		return appendObject(output, map[string]any(typed), depth)
	case map[string]any:
		return appendObject(output, typed, depth)
	case []any:
		return appendArray(output, typed, depth)
	case []string:
		values := make([]any, len(typed))
		for index := range typed {
			values[index] = typed[index]
		}
		return appendArray(output, values, depth)
	case string:
		return appendString(output, typed)
	case int:
		return appendInteger(output, int64(typed))
	case int8:
		return appendInteger(output, int64(typed))
	case int16:
		return appendInteger(output, int64(typed))
	case int32:
		return appendInteger(output, int64(typed))
	case int64:
		return appendInteger(output, typed)
	case uint:
		if uint64(typed) > uint64(MaxExactInteger) {
			return errUnsupported
		}
		return appendInteger(output, int64(typed))
	case uint8:
		return appendInteger(output, int64(typed))
	case uint16:
		return appendInteger(output, int64(typed))
	case uint32:
		return appendInteger(output, int64(typed))
	case uint64:
		if typed > uint64(MaxExactInteger) {
			return errUnsupported
		}
		return appendInteger(output, int64(typed))
	default:
		return errUnsupported
	}
}

func appendObject(output *bytes.Buffer, object map[string]any, depth int) error {
	keys := make([]string, 0, len(object))
	for key := range object {
		if !utf8.ValidString(key) {
			return errUnsupported
		}
		keys = append(keys, key)
	}
	jsorder.Sort(keys)
	if len(keys) == 0 {
		output.WriteString("{}")
		return nil
	}
	output.WriteString("{\n")
	for index, key := range keys {
		indent(output, depth+1)
		if err := appendString(output, key); err != nil {
			return err
		}
		output.WriteString(": ")
		if err := appendValue(output, object[key], depth+1); err != nil {
			return err
		}
		if index+1 != len(keys) {
			output.WriteByte(',')
		}
		output.WriteByte('\n')
	}
	indent(output, depth)
	output.WriteByte('}')
	return nil
}

func appendArray(output *bytes.Buffer, values []any, depth int) error {
	if len(values) == 0 {
		output.WriteString("[]")
		return nil
	}
	output.WriteString("[\n")
	for index, value := range values {
		indent(output, depth+1)
		if err := appendValue(output, value, depth+1); err != nil {
			return err
		}
		if index+1 != len(values) {
			output.WriteByte(',')
		}
		output.WriteByte('\n')
	}
	indent(output, depth)
	output.WriteByte(']')
	return nil
}

func appendString(output *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return errUnsupported
	}
	const hex = "0123456789abcdef"
	output.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			output.WriteByte('\\')
			output.WriteRune(r)
		case '\b':
			output.WriteString("\\b")
		case '\f':
			output.WriteString("\\f")
		case '\n':
			output.WriteString("\\n")
		case '\r':
			output.WriteString("\\r")
		case '\t':
			output.WriteString("\\t")
		default:
			if r < 0x20 {
				output.WriteString("\\u00")
				output.WriteByte(hex[byte(r)>>4])
				output.WriteByte(hex[byte(r)&0x0f])
			} else {
				output.WriteRune(r)
			}
		}
	}
	output.WriteByte('"')
	return nil
}

func appendInteger(output *bytes.Buffer, value int64) error {
	if value < -MaxExactInteger || value > MaxExactInteger {
		return errUnsupported
	}
	output.WriteString(strconv.FormatInt(value, 10))
	return nil
}

func indent(output *bytes.Buffer, depth int) {
	for range depth {
		output.WriteString("  ")
	}
}
