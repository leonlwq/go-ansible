package util

import (
	"reflect"
	"strings"
)

// ToBool parses common representations of boolean values in a case-insensitive manner.
// Recognizes "true", "yes", "1", "on", "t", "y" as true; "false", "no", "0", "off", "f", "n", "" as false.
func ToBool(v interface{}) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		switch s {
		case "true", "yes", "1", "on", "t", "y":
			return true
		default:
			return false
		}
	case int:
		return val != 0
	case int8:
		return val != 0
	case int16:
		return val != 0
	case int32:
		return val != 0
	case int64:
		return val != 0
	case uint:
		return val != 0
	case uint8:
		return val != 0
	case uint16:
		return val != 0
	case uint32:
		return val != 0
	case uint64:
		return val != 0
	case float32:
		return val != 0
	case float64:
		return val != 0
	default:
		return false
	}
}

// IsTruthy evaluates whether a value is considered "truthy" in Ansible semantics.
// Empty strings, false, 0, nil, "false", "no", "0", "none", "null", and empty collections are false.
func IsTruthy(value interface{}) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s != "" && s != "false" && s != "no" && s != "0" && s != "none" && s != "null"
	case int:
		return v != 0
	case int8:
		return v != 0
	case int16:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case uint:
		return v != 0
	case uint8:
		return v != 0
	case uint16:
		return v != 0
	case uint32:
		return v != 0
	case uint64:
		return v != 0
	case float32:
		return v != 0
	case float64:
		return v != 0
	case []interface{}:
		return len(v) > 0
	case []string:
		return len(v) > 0
	case map[string]interface{}:
		return len(v) > 0
	case map[string]string:
		return len(v) > 0
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	case reflect.Pointer, reflect.Interface:
		return !rv.IsNil()
	default:
		return true
	}
}

