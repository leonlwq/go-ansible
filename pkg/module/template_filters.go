package module

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

func templateFuncMap() template.FuncMap {
	return template.FuncMap{
		"default":    TemplateDefaultValue,
		"ternary":    TemplateTernary,
		"dict2items": TemplateDict2Items,
		"quote":      TemplateQuoteValue,
		"upper": func(value interface{}) string {
			return strings.ToUpper(fmt.Sprintf("%v", value))
		},
		"lower": func(value interface{}) string {
			return strings.ToLower(fmt.Sprintf("%v", value))
		},
		"trim": func(value interface{}) string {
			return strings.TrimSpace(fmt.Sprintf("%v", value))
		},
		"replace": func(oldValue interface{}, newValue interface{}, value interface{}) string {
			return strings.ReplaceAll(fmt.Sprintf("%v", value), fmt.Sprintf("%v", oldValue), fmt.Sprintf("%v", newValue))
		},
		"join": func(sep interface{}, value interface{}) string {
			items := TemplateNormalizeList(value)
			return strings.Join(items, fmt.Sprintf("%v", sep))
		},
		"split": func(sep interface{}, value interface{}) []string {
			return strings.Split(fmt.Sprintf("%v", value), fmt.Sprintf("%v", sep))
		},
		"length": TemplateLength,
		"first":  TemplateFirst,
		"last":   TemplateLast,
		"to_json": func(value interface{}) string {
			data, err := json.Marshal(value)
			if err != nil {
				return ""
			}
			return string(data)
		},
		"from_json": func(value interface{}) interface{} {
			var decoded interface{}
			if err := json.Unmarshal([]byte(fmt.Sprintf("%v", value)), &decoded); err != nil {
				return nil
			}
			return decoded
		},
		"basename": func(value interface{}) string {
			return filepath.Base(fmt.Sprintf("%v", value))
		},
		"dirname": func(value interface{}) string {
			return filepath.Dir(fmt.Sprintf("%v", value))
		},
		"b64encode": func(value interface{}) string {
			return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%v", value)))
		},
		"b64decode": func(value interface{}) string {
			data, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", value))
			if err != nil {
				return ""
			}
			return string(data)
		},
		// High-frequency Ansible filters
		"combine":       TemplateCombine,
		"regex_replace": TemplateRegexReplace,
		"regex_search":  TemplateRegexSearch,
		"regex_findall": TemplateRegexFindAll,
		"selectattr":    TemplateSelectAttr,
		"rejectattr":    TemplateRejectAttr,
		"map":           TemplateMap,
		"ipaddr":        TemplateIpAddr,
		"flatten":       TemplateFlatten,
		"unique":        TemplateUnique,
		"difference":    TemplateDifference,
		"intersect":     TemplateIntersect,
		"union":         TemplateUnion,
		"min":           TemplateMin,
		"max":           TemplateMax,
		"sum":           TemplateSum,
		"bool":          TemplateBool,
		"int":           TemplateInt,
		"float":         TemplateFloat,
		"abs":           TemplateAbs,
		"hash":          TemplateHash,
	}
}

func TemplateDefaultValue(args ...interface{}) interface{} {
	if len(args) == 0 {
		return nil
	}
	if len(args) == 1 {
		return args[0]
	}

	defaultValue := args[0]
	value := args[len(args)-1]
	boolean := false
	if len(args) > 2 {
		boolArg := args[1]
		if b, ok := boolArg.(bool); ok {
			boolean = b
		} else if s, ok := boolArg.(string); ok {
			s = strings.TrimSpace(strings.ToLower(s))
			if strings.HasPrefix(s, "boolean=") {
				s = strings.TrimPrefix(s, "boolean=")
			}
			boolean = (s == "true" || s == "yes" || s == "1")
		}
	}

	if !boolean {
		if value == nil {
			return defaultValue
		}
		if s, ok := value.(string); ok && strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
			return defaultValue
		}
		return value
	}

	if !TemplateIsTruthy(value) {
		return defaultValue
	}
	return value
}

func TemplateTernary(args ...interface{}) interface{} {
	if len(args) < 3 {
		return nil
	}
	trueVal := args[0]
	falseVal := args[1]
	var noneVal interface{}
	hasNoneVal := false
	var cond interface{}

	if len(args) == 3 {
		cond = args[2]
	} else {
		noneVal = args[2]
		hasNoneVal = true
		cond = args[3]
	}

	if hasNoneVal && cond == nil {
		return noneVal
	}
	if TemplateIsTruthy(cond) {
		return trueVal
	}
	return falseVal
}

func TemplateIsTruthy(value interface{}) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		s := strings.TrimSpace(strings.ToLower(v))
		return s != "" && s != "false" && s != "no" && s != "0" && s != "none" && s != "null"
	}
	return !TemplateIsEmpty(value)
}

func TemplateNormalizeList(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, fmt.Sprintf("%v", item))
		}
		return items
	case []interface{}:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, fmt.Sprintf("%v", item))
		}
		return items
	default:
		if value == nil {
			return nil
		}
		return []string{fmt.Sprintf("%v", value)}
	}
}

func TemplateQuoteValue(value interface{}) string {
	return shellQuote(fmt.Sprintf("%v", value))
}

func TemplateLength(value interface{}) int {
	switch v := value.(type) {
	case nil:
		return 0
	case string:
		return len(v)
	case []interface{}:
		return len(v)
	case []string:
		return len(v)
	case map[string]interface{}:
		return len(v)
	case map[string]string:
		return len(v)
	default:
		return len(TemplateNormalizeList(value))
	}
}

func TemplateFirst(value interface{}) interface{} {
	item, ok := TemplateIndex(value, 0)
	if !ok {
		return nil
	}
	return item
}

func TemplateLast(value interface{}) interface{} {
	length := TemplateLength(value)
	if length == 0 {
		return nil
	}
	item, ok := TemplateIndex(value, length-1)
	if !ok {
		return nil
	}
	return item
}

func TemplateIndex(value interface{}, index int) (interface{}, bool) {
	if index < 0 {
		return nil, false
	}
	switch v := value.(type) {
	case string:
		runes := []rune(v)
		if index >= len(runes) {
			return nil, false
		}
		return string(runes[index]), true
	case []interface{}:
		if index >= len(v) {
			return nil, false
		}
		return v[index], true
	case []string:
		if index >= len(v) {
			return nil, false
		}
		return v[index], true
	default:
		items := TemplateNormalizeList(value)
		if index >= len(items) {
			return nil, false
		}
		return items[index], true
	}
}

func TemplateIsEmpty(value interface{}) bool {
	if value == nil {
		return true
	}

	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) == ""
	case bool:
		return !v
	case int:
		return v == 0
	case int8:
		return v == 0
	case int16:
		return v == 0
	case int32:
		return v == 0
	case int64:
		return v == 0
	case uint:
		return v == 0
	case uint8:
		return v == 0
	case uint16:
		return v == 0
	case uint32:
		return v == 0
	case uint64:
		return v == 0
	case float32:
		return v == 0
	case float64:
		return v == 0
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}

	return false
}

// TemplateCombine merges dictionaries. Supports multiple maps or slice of maps, and recursive merge.
func TemplateCombine(args ...interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	recursive := false

	var mapsToMerge []map[string]interface{}
	for _, arg := range args {
		if b, ok := arg.(bool); ok {
			recursive = b
			continue
		}
		if str, ok := arg.(string); ok && strings.HasPrefix(str, "recursive=") {
			val := strings.TrimPrefix(str, "recursive=")
			recursive = val == "true" || val == "True" || val == "yes" || val == "1"
			continue
		}
		if slice, ok := toInterfaceSlice(arg); ok {
			for _, item := range slice {
				if m := toMapStringInterface(item); m != nil {
					mapsToMerge = append(mapsToMerge, m)
				}
			}
			continue
		}
		if m := toMapStringInterface(arg); m != nil {
			mapsToMerge = append(mapsToMerge, m)
		}
	}

	for _, m := range mapsToMerge {
		deepMergeMaps(result, m, recursive)
	}

	return result, nil
}

func deepMergeMaps(dest, src map[string]interface{}, recursive bool) {
	for k, v := range src {
		if recursive {
			if destVal, ok := dest[k]; ok {
				destMap := toMapStringInterface(destVal)
				srcMap := toMapStringInterface(v)
				if destMap != nil && srcMap != nil {
					deepMergeMaps(destMap, srcMap, true)
					dest[k] = destMap
					continue
				}
			}
		}
		dest[k] = v
	}
}

// TemplateRegexReplace replaces matches with replacement.
func TemplateRegexReplace(pattern, replacement, value interface{}) string {
	valStr := fmt.Sprintf("%v", value)
	re, err := regexp.Compile(fmt.Sprintf("%v", pattern))
	if err != nil {
		return valStr
	}
	return re.ReplaceAllString(valStr, fmt.Sprintf("%v", replacement))
}

// TemplateRegexSearch returns the first matching substring.
func TemplateRegexSearch(pattern, value interface{}) string {
	re, err := regexp.Compile(fmt.Sprintf("%v", pattern))
	if err != nil {
		return ""
	}
	return re.FindString(fmt.Sprintf("%v", value))
}

// TemplateRegexFindAll returns all regex matches.
func TemplateRegexFindAll(pattern, value interface{}) []string {
	re, err := regexp.Compile(fmt.Sprintf("%v", pattern))
	if err != nil {
		return []string{}
	}
	res := re.FindAllString(fmt.Sprintf("%v", value), -1)
	if res == nil {
		return []string{}
	}
	return res
}

// TemplateSelectAttr filters list elements where an attribute matches criteria.
func TemplateSelectAttr(attr interface{}, rest ...interface{}) []interface{} {
	var value interface{}
	var op string
	var target interface{}

	attrName := fmt.Sprintf("%v", attr)
	if len(rest) == 1 {
		value = rest[0]
	} else if len(rest) == 2 {
		op = fmt.Sprintf("%v", rest[0])
		value = rest[1]
	} else if len(rest) >= 3 {
		op = fmt.Sprintf("%v", rest[0])
		target = rest[1]
		value = rest[len(rest)-1]
	}

	items, ok := toInterfaceSlice(value)
	if !ok {
		return []interface{}{}
	}

	result := make([]interface{}, 0)
	for _, item := range items {
		val, exists := extractItemAttr(item, attrName)
		match := false
		switch op {
		case "", "defined":
			match = exists && !TemplateIsEmpty(val)
		case "undefined":
			match = !exists
		case "equalto", "==":
			match = exists && fmt.Sprintf("%v", val) == fmt.Sprintf("%v", target)
		case "notequalto", "!=":
			match = !exists || fmt.Sprintf("%v", val) != fmt.Sprintf("%v", target)
		case "match":
			if exists {
				if re, err := regexp.Compile(fmt.Sprintf("%v", target)); err == nil {
					match = re.MatchString(fmt.Sprintf("%v", val))
				}
			}
		default:
			match = exists && !TemplateIsEmpty(val)
		}
		if match {
			result = append(result, item)
		}
	}
	return result
}

// TemplateRejectAttr is the inverse of selectattr.
func TemplateRejectAttr(attr interface{}, rest ...interface{}) []interface{} {
	selected := TemplateSelectAttr(attr, rest...)
	items, ok := toInterfaceSlice(rest[len(rest)-1])
	if !ok {
		return []interface{}{}
	}
	selectedSet := make(map[string]bool)
	for _, s := range selected {
		selectedSet[fmt.Sprintf("%v", s)] = true
	}
	result := make([]interface{}, 0)
	for _, item := range items {
		if !selectedSet[fmt.Sprintf("%v", item)] {
			result = append(result, item)
		}
	}
	return result
}

// TemplateMap extracts an attribute from each list item or applies a string transform.
func TemplateMap(arg interface{}, rest ...interface{}) []interface{} {
	var value interface{}
	var attr string
	first := fmt.Sprintf("%v", arg)
	if len(rest) == 1 {
		value = rest[0]
	} else if len(rest) >= 2 {
		attr = fmt.Sprintf("%v", rest[0])
		value = rest[len(rest)-1]
	}

	items, ok := toInterfaceSlice(value)
	if !ok {
		return []interface{}{}
	}

	result := make([]interface{}, 0, len(items))
	for _, item := range items {
		if first == "attribute" && attr != "" {
			val, _ := extractItemAttr(item, attr)
			result = append(result, val)
		} else {
			switch first {
			case "upper":
				result = append(result, strings.ToUpper(fmt.Sprintf("%v", item)))
			case "lower":
				result = append(result, strings.ToLower(fmt.Sprintf("%v", item)))
			case "trim":
				result = append(result, strings.TrimSpace(fmt.Sprintf("%v", item)))
			default:
				val, _ := extractItemAttr(item, first)
				result = append(result, val)
			}
		}
	}
	return result
}

// TemplateIpAddr performs IP address queries and validity checks.
func TemplateIpAddr(args ...interface{}) interface{} {
	var query string
	var val interface{}
	if len(args) == 1 {
		val = args[0]
	} else if len(args) >= 2 {
		query = fmt.Sprintf("%v", args[0])
		val = args[1]
	}

	s := strings.TrimSpace(fmt.Sprintf("%v", val))
	ip := net.ParseIP(s)
	var ipNet *net.IPNet
	if ip == nil {
		var err error
		ip, ipNet, err = net.ParseCIDR(s)
		if err != nil {
			return false
		}
	}

	switch strings.ToLower(query) {
	case "", "bool":
		return true
	case "ipv4", "4":
		return ip.To4() != nil
	case "ipv6", "6":
		return ip.To4() == nil && ip.To16() != nil
	case "address":
		return ip.String()
	case "netmask":
		if ipNet != nil {
			return fmt.Sprintf("%d.%d.%d.%d", ipNet.Mask[0], ipNet.Mask[1], ipNet.Mask[2], ipNet.Mask[3])
		}
		if ip.To4() != nil {
			return "255.255.255.255"
		}
		return ""
	default:
		return true
	}
}

// TemplateFlatten recursively flattens slices.
func TemplateFlatten(value interface{}) []interface{} {
	var result []interface{}
	var flattenHelper func(v interface{})
	flattenHelper = func(v interface{}) {
		if slice, ok := toInterfaceSlice(v); ok {
			for _, item := range slice {
				flattenHelper(item)
			}
		} else {
			result = append(result, v)
		}
	}
	flattenHelper(value)
	return result
}

// TemplateUnique removes duplicates from a slice while preserving order.
func TemplateUnique(value interface{}) []interface{} {
	items, ok := toInterfaceSlice(value)
	if !ok {
		return []interface{}{}
	}
	seen := make(map[string]bool)
	result := make([]interface{}, 0)
	for _, item := range items {
		key := fmt.Sprintf("%v", item)
		if !seen[key] {
			seen[key] = true
			result = append(result, item)
		}
	}
	return result
}

// TemplateDifference returns elements in value that are not in other.
func TemplateDifference(other, value interface{}) []interface{} {
	items, ok := toInterfaceSlice(value)
	if !ok {
		return []interface{}{}
	}
	otherItems, _ := toInterfaceSlice(other)
	otherSet := make(map[string]bool)
	for _, o := range otherItems {
		otherSet[fmt.Sprintf("%v", o)] = true
	}
	result := make([]interface{}, 0)
	for _, item := range items {
		if !otherSet[fmt.Sprintf("%v", item)] {
			result = append(result, item)
		}
	}
	return result
}

// TemplateIntersect returns elements common to both value and other.
func TemplateIntersect(other, value interface{}) []interface{} {
	items, ok := toInterfaceSlice(value)
	if !ok {
		return []interface{}{}
	}
	otherItems, _ := toInterfaceSlice(other)
	otherSet := make(map[string]bool)
	for _, o := range otherItems {
		otherSet[fmt.Sprintf("%v", o)] = true
	}
	seen := make(map[string]bool)
	result := make([]interface{}, 0)
	for _, item := range items {
		k := fmt.Sprintf("%v", item)
		if otherSet[k] && !seen[k] {
			seen[k] = true
			result = append(result, item)
		}
	}
	return result
}

// TemplateUnion returns the union of value and other with duplicates removed.
func TemplateUnion(other, value interface{}) []interface{} {
	items1, _ := toInterfaceSlice(value)
	items2, _ := toInterfaceSlice(other)
	combined := append(items1, items2...)
	return TemplateUnique(combined)
}

// TemplateMin returns the minimum number from a slice.
func TemplateMin(value interface{}) interface{} {
	items, ok := toInterfaceSlice(value)
	if !ok || len(items) == 0 {
		return nil
	}
	minVal := math.MaxFloat64
	allInt := true
	for _, item := range items {
		f, ok := toFloat(item)
		if !ok {
			continue
		}
		if _, isInt := item.(int); !isInt {
			allInt = false
		}
		if f < minVal {
			minVal = f
		}
	}
	if allInt {
		return int(minVal)
	}
	return minVal
}

// TemplateMax returns the maximum number from a slice.
func TemplateMax(value interface{}) interface{} {
	items, ok := toInterfaceSlice(value)
	if !ok || len(items) == 0 {
		return nil
	}
	maxVal := -math.MaxFloat64
	allInt := true
	for _, item := range items {
		f, ok := toFloat(item)
		if !ok {
			continue
		}
		if _, isInt := item.(int); !isInt {
			allInt = false
		}
		if f > maxVal {
			maxVal = f
		}
	}
	if allInt {
		return int(maxVal)
	}
	return maxVal
}

// TemplateSum returns the sum of numbers from a slice.
func TemplateSum(value interface{}) interface{} {
	items, ok := toInterfaceSlice(value)
	if !ok || len(items) == 0 {
		return 0
	}
	var total float64
	allInt := true
	for _, item := range items {
		f, ok := toFloat(item)
		if !ok {
			continue
		}
		if _, isInt := item.(int); !isInt {
			allInt = false
		}
		total += f
	}
	if allInt {
		return int(total)
	}
	return total
}

// TemplateBool converts value to boolean.
func TemplateBool(value interface{}) bool {
	if TemplateIsEmpty(value) {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", value)))
	switch s {
	case "true", "yes", "1", "y", "t":
		return true
	default:
		return false
	}
}

// TemplateInt converts value to int.
func TemplateInt(args ...interface{}) int {
	defaultVal := 0
	var val interface{}
	if len(args) == 1 {
		val = args[0]
	} else if len(args) >= 2 {
		if d, ok := args[0].(int); ok {
			defaultVal = d
		} else if di, err := strconv.Atoi(fmt.Sprintf("%v", args[0])); err == nil {
			defaultVal = di
		}
		val = args[1]
	}
	if i, ok := val.(int); ok {
		return i
	}
	if i, err := strconv.Atoi(fmt.Sprintf("%v", val)); err == nil {
		return i
	}
	return defaultVal
}

// TemplateFloat converts value to float.
func TemplateFloat(args ...interface{}) float64 {
	defaultVal := 0.0
	var val interface{}
	if len(args) == 1 {
		val = args[0]
	} else if len(args) >= 2 {
		if d, ok := args[0].(float64); ok {
			defaultVal = d
		} else if df, err := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64); err == nil {
			defaultVal = df
		}
		val = args[1]
	}
	if f, ok := val.(float64); ok {
		return f
	}
	if f, err := strconv.ParseFloat(fmt.Sprintf("%v", val), 64); err == nil {
		return f
	}
	return defaultVal
}

// TemplateAbs returns the absolute value.
func TemplateAbs(value interface{}) interface{} {
	if i, ok := value.(int); ok {
		if i < 0 {
			return -i
		}
		return i
	}
	f, _ := toFloat(value)
	return math.Abs(f)
}

// TemplateHash calculates cryptographic hashes (md5, sha1, sha256, sha512).
func TemplateHash(algo, value interface{}) string {
	data := []byte(fmt.Sprintf("%v", value))
	switch strings.ToLower(fmt.Sprintf("%v", algo)) {
	case "md5":
		h := md5.Sum(data)
		return hex.EncodeToString(h[:])
	case "sha1":
		h := sha1.Sum(data)
		return hex.EncodeToString(h[:])
	case "sha256":
		h := sha256.Sum256(data)
		return hex.EncodeToString(h[:])
	case "sha512":
		h := sha512.Sum512(data)
		return hex.EncodeToString(h[:])
	default:
		h := sha256.Sum256(data)
		return hex.EncodeToString(h[:])
	}
}

// Internal helpers

func toInterfaceSlice(value interface{}) ([]interface{}, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case []interface{}:
		return v, true
	case []string:
		res := make([]interface{}, len(v))
		for i, s := range v {
			res[i] = s
		}
		return res, true
	case []int:
		res := make([]interface{}, len(v))
		for i, n := range v {
			res[i] = n
		}
		return res, true
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		n := rv.Len()
		res := make([]interface{}, n)
		for i := 0; i < n; i++ {
			res[i] = rv.Index(i).Interface()
		}
		return res, true
	}
	return nil, false
}

func toMapStringInterface(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case map[string]interface{}:
		return v
	case map[string]string:
		res := make(map[string]interface{}, len(v))
		for k, val := range v {
			res[k] = val
		}
		return res
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Map {
		res := make(map[string]interface{})
		iter := rv.MapRange()
		for iter.Next() {
			res[fmt.Sprintf("%v", iter.Key().Interface())] = iter.Value().Interface()
		}
		return res
	}
	return nil
}

func extractItemAttr(item interface{}, attr string) (interface{}, bool) {
	if item == nil {
		return nil, false
	}
	if m := toMapStringInterface(item); m != nil {
		val, ok := m[attr]
		return val, ok
	}
	rv := reflect.ValueOf(item)
	if rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		field := rv.FieldByName(attr)
		if field.IsValid() {
			return field.Interface(), true
		}
		for i := 0; i < rv.NumField(); i++ {
			if strings.EqualFold(rv.Type().Field(i).Name, attr) {
				return rv.Field(i).Interface(), true
			}
		}
	}
	return nil, false
}

func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// TemplateDict2Items 将字典或映射转换为 Ansible 标准 [{"key": k, "value": v}] 列表
func TemplateDict2Items(val interface{}) ([]map[string]interface{}, error) {
	if val == nil {
		return []map[string]interface{}{}, nil
	}

	result := make([]map[string]interface{}, 0)
	switch m := val.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			result = append(result, map[string]interface{}{
				"key":   k,
				"value": m[k],
			})
		}
		return result, nil
	case map[string]string:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			result = append(result, map[string]interface{}{
				"key":   k,
				"value": m[k],
			})
		}
		return result, nil
	default:
		rv := reflect.ValueOf(val)
		if rv.Kind() == reflect.Map {
			mapKeys := rv.MapKeys()
			keys := make([]string, 0, len(mapKeys))
			for _, mk := range mapKeys {
				keys = append(keys, fmt.Sprintf("%v", mk.Interface()))
			}
			sort.Strings(keys)
			for _, k := range keys {
				var valVal reflect.Value
				for _, mk := range mapKeys {
					if fmt.Sprintf("%v", mk.Interface()) == k {
						valVal = rv.MapIndex(mk)
						break
					}
				}
				var iv interface{}
				if valVal.IsValid() {
					iv = valVal.Interface()
				}
				result = append(result, map[string]interface{}{
					"key":   k,
					"value": iv,
				})
			}
			return result, nil
		}
		return nil, fmt.Errorf("dict2items requires a dictionary, got %T", val)
	}
}
