package module

import (
	"crypto/md5"
	crand "crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"

	"go-ansible/pkg/util"
)

var (
	defaultFuncMapOnce sync.Once
	cachedDefaultFuncMap template.FuncMap
)

func templateFuncMap() template.FuncMap {
	defaultFuncMapOnce.Do(func() {
		cachedDefaultFuncMap = buildTemplateFuncMap()
	})
	return cachedDefaultFuncMap
}

func buildTemplateFuncMap() template.FuncMap {
	return template.FuncMap{
		"lookup":       TemplateLookup,
		"default":      TemplateDefaultValue,
		"mandatory":    TemplateMandatory,
		"ternary":      TemplateTernary,
		"dict2items":   TemplateDict2Items,
		"items2dict":   TemplateItems2DictSimple,
		"to_yaml":      TemplateToYAML,
		"to_nice_yaml": TemplateToNiceYAML,
		"from_yaml":    TemplateFromYAML,
		"quote":        TemplateQuoteValue,
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
		"split": func(args ...interface{}) []string {
			if len(args) == 0 {
				return []string{}
			}
			if len(args) == 1 {
				return strings.Fields(fmt.Sprintf("%v", args[0]))
			}
			sep := fmt.Sprintf("%v", args[0])
			val := fmt.Sprintf("%v", args[1])
			return strings.Split(val, sep)
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
		"to_nice_json": func(value interface{}, args ...interface{}) string {
			indent := "  "
			if len(args) > 0 {
				if n, ok := args[0].(int); ok && n >= 0 {
					indent = strings.Repeat(" ", n)
				}
			}
			data, err := json.MarshalIndent(value, "", indent)
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
			if b, ok := value.([]byte); ok {
				return base64.StdEncoding.EncodeToString(b)
			}
			return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%v", value)))
		},
		"b64decode": func(value interface{}) string {
			s := strings.TrimSpace(fmt.Sprintf("%v", value))
			data, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return s
			}
			return string(data)
		},
		// High-frequency Ansible filters
		"to_datetime":   TemplateToDatetime,
		"strftime":      TemplateStrftime,
		"urlencode":     TemplateUrlencode,
		"type_debug":    TemplateTypeDebug,
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
		"hash":            TemplateHash,
		"checksum":        TemplateChecksum,
		"urlsplit":        TemplateUrlsplit,
		"version":         TemplateVersionCompare,
		"version_compare": TemplateVersionCompare,
	}
}

// TemplateVersionCompare 实现 Ansible version 与 version_compare 过滤器
func TemplateVersionCompare(args ...interface{}) bool {
	if len(args) == 0 {
		return false
	}
	if len(args) == 1 {
		return true
	}
	var v1, v2, op string
	if len(args) == 2 {
		v1 = fmt.Sprintf("%v", args[1])
		v2 = fmt.Sprintf("%v", args[0])
		op = "=="
	} else {
		a1 := strings.TrimSpace(fmt.Sprintf("%v", args[1]))
		switch strings.ToLower(a1) {
		case "==", "!=", ">", ">=", "<", "<=", "=", "eq", "ne", "gt", "ge", "lt", "le":
			op = a1
			v2 = fmt.Sprintf("%v", args[0])
			v1 = fmt.Sprintf("%v", args[2])
		default:
			a2 := strings.TrimSpace(fmt.Sprintf("%v", args[2]))
			switch strings.ToLower(a2) {
			case "==", "!=", ">", ">=", "<", "<=", "=", "eq", "ne", "gt", "ge", "lt", "le":
				v1 = fmt.Sprintf("%v", args[0])
				v2 = fmt.Sprintf("%v", args[1])
				op = a2
			default:
				v1 = fmt.Sprintf("%v", args[len(args)-1])
				v2 = fmt.Sprintf("%v", args[0])
				op = fmt.Sprintf("%v", args[1])
			}
		}
	}
	return util.CompareVersions(v1, v2, op)
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

var regexCache = util.RegexPool()

func getCachedRegex(pattern string) (*regexp.Regexp, error) {
	return util.GetRegex(pattern)
}

// TemplateMandatory raises an error if the value is empty, nil, or undefined.
func TemplateMandatory(args ...interface{}) (interface{}, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("mandatory filter requires a value")
	}
	val := args[0]
	msg := "Mandatory variable is not defined or is empty"
	if len(args) > 1 {
		msg = fmt.Sprintf("%v", args[1])
	}
	if val == nil {
		return nil, fmt.Errorf("%s", msg)
	}
	if s, ok := val.(string); ok && strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("%s", msg)
	}
	return val, nil
}

func convertPythonBackreferences(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	// 将 Python 正则反向引用形如 \g<1> 转换为 Go 的 ${1}
	reNamed, err := getCachedRegex(`\\g<([0-9a-zA-Z_]+)>`)
	if err == nil {
		s = reNamed.ReplaceAllString(s, `${$1}`)
	}
	// 将 Python 正则反向引用形如 \1, \2 转换为 Go 的 $1, $2
	reNum, err := getCachedRegex(`\\([0-9]+)`)
	if err == nil {
		s = reNum.ReplaceAllString(s, `$$1`)
	}
	return s
}

// TemplateRegexReplace replaces matches with replacement.
func TemplateRegexReplace(pattern, replacement, value interface{}) string {
	valStr := fmt.Sprintf("%v", value)
	re, err := getCachedRegex(fmt.Sprintf("%v", pattern))
	if err != nil {
		return valStr
	}
	replStr := convertPythonBackreferences(fmt.Sprintf("%v", replacement))
	return re.ReplaceAllString(valStr, replStr)
}

// TemplateRegexSearch returns the first matching substring.
func TemplateRegexSearch(pattern, value interface{}) string {
	re, err := getCachedRegex(fmt.Sprintf("%v", pattern))
	if err != nil {
		return ""
	}
	return re.FindString(fmt.Sprintf("%v", value))
}

// TemplateRegexFindAll returns all regex matches.
func TemplateRegexFindAll(pattern, value interface{}) []string {
	re, err := getCachedRegex(fmt.Sprintf("%v", pattern))
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
				if re, err := getCachedRegex(fmt.Sprintf("%v", target)); err == nil {
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
// Supports TemplateHash(value) with default 'sha1' (Ansible standard), or TemplateHash(algo, value) / TemplateHash(value, algo).
func TemplateHash(args ...interface{}) string {
	if len(args) == 0 {
		return ""
	}
	algo := "sha1"
	var value interface{}

	if len(args) == 1 {
		value = args[0]
	} else {
		a0 := strings.ToLower(fmt.Sprintf("%v", args[0]))
		switch a0 {
		case "md5", "sha1", "sha256", "sha512":
			algo = a0
			value = args[1]
		default:
			a1 := strings.ToLower(fmt.Sprintf("%v", args[1]))
			switch a1 {
			case "md5", "sha1", "sha256", "sha512":
				algo = a1
				value = args[0]
			default:
				algo = a0
				value = args[1]
			}
		}
	}

	data := []byte(fmt.Sprintf("%v", value))
	switch algo {
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
		h := sha1.Sum(data)
		return hex.EncodeToString(h[:])
	}
}

// TemplateChecksum returns sha1 hash of value (Ansible standard checksum filter).
func TemplateChecksum(value interface{}) string {
	return TemplateHash("sha1", value)
}

// TemplateUrlsplit parses a URL and returns a map of its components or a specific component.
func TemplateUrlsplit(args ...interface{}) interface{} {
	if len(args) == 0 {
		return nil
	}
	var rawURL, queryKey string
	if len(args) == 1 {
		rawURL = fmt.Sprintf("%v", args[0])
	} else {
		arg0 := fmt.Sprintf("%v", args[0])
		arg1 := fmt.Sprintf("%v", args[1])
		if strings.Contains(arg0, "://") || strings.HasPrefix(arg0, "/") {
			rawURL = arg0
			queryKey = arg1
		} else {
			queryKey = arg0
			rawURL = arg1
		}
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		if queryKey != "" {
			return ""
		}
		return map[string]interface{}{}
	}

	var user, pass string
	netloc := u.Host
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
		netloc = u.User.String() + "@" + u.Host
	}

	var portVal interface{}
	if pStr := u.Port(); pStr != "" {
		if pInt, err := strconv.Atoi(pStr); err == nil {
			portVal = pInt
		} else {
			portVal = pStr
		}
	}

	m := map[string]interface{}{
		"scheme":   u.Scheme,
		"netloc":   netloc,
		"path":     u.Path,
		"query":    u.RawQuery,
		"fragment": u.Fragment,
		"username": user,
		"password": pass,
		"hostname": u.Hostname(),
		"port":     portVal,
	}

	if queryKey != "" {
		if v, ok := m[queryKey]; ok {
			return v
		}
		return ""
	}
	return m
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

// TemplateItems2Dict converts a list of key/value pairs into a dictionary
func TemplateItems2Dict(val interface{}, args ...string) (map[string]interface{}, error) {
	if val == nil {
		return map[string]interface{}{}, nil
	}
	keyName := "key"
	valueName := "value"
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		arg = strings.Trim(arg, `"'`)
		if strings.HasPrefix(strings.ToLower(arg), "key_name=") {
			keyName = strings.Trim(arg[len("key_name="):], `"'`)
		} else if strings.HasPrefix(strings.ToLower(arg), "value_name=") {
			valueName = strings.Trim(arg[len("value_name="):], `"'`)
		} else if keyName == "key" {
			keyName = arg
		} else if valueName == "value" {
			valueName = arg
		}
	}

	result := make(map[string]interface{})

	extractKV := func(item interface{}) (string, interface{}, bool) {
		if item == nil {
			return "", nil, false
		}
		switch m := item.(type) {
		case map[string]interface{}:
			k, kOk := m[keyName]
			v := m[valueName]
			if kOk && k != nil {
				return fmt.Sprintf("%v", k), v, true
			}
		case map[string]string:
			k, kOk := m[keyName]
			v := m[valueName]
			if kOk {
				return k, v, true
			}
		case map[interface{}]interface{}:
			var kVal, vVal interface{}
			var kFound bool
			for mk, mv := range m {
				if fmt.Sprintf("%v", mk) == keyName {
					kVal = mv
					kFound = true
				}
				if fmt.Sprintf("%v", mk) == valueName {
					vVal = mv
				}
			}
			if kFound && kVal != nil {
				return fmt.Sprintf("%v", kVal), vVal, true
			}
		default:
			rv := reflect.ValueOf(item)
			if rv.Kind() == reflect.Map {
				var kVal, vVal interface{}
				var kFound bool
				for _, mk := range rv.MapKeys() {
					if fmt.Sprintf("%v", mk.Interface()) == keyName {
						kVal = rv.MapIndex(mk).Interface()
						kFound = true
					}
					if fmt.Sprintf("%v", mk.Interface()) == valueName {
						vVal = rv.MapIndex(mk).Interface()
					}
				}
				if kFound && kVal != nil {
					return fmt.Sprintf("%v", kVal), vVal, true
				}
			}
		}
		return "", nil, false
	}

	switch items := val.(type) {
	case []interface{}:
		for _, item := range items {
			if k, v, ok := extractKV(item); ok {
				result[k] = v
			}
		}
		return result, nil
	case []map[string]interface{}:
		for _, item := range items {
			if k, v, ok := extractKV(item); ok {
				result[k] = v
			}
		}
		return result, nil
	case []map[string]string:
		for _, item := range items {
			if k, v, ok := extractKV(item); ok {
				result[k] = v
			}
		}
		return result, nil
	default:
		rv := reflect.ValueOf(val)
		if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			for i := 0; i < rv.Len(); i++ {
				item := rv.Index(i).Interface()
				if k, v, ok := extractKV(item); ok {
					result[k] = v
				}
			}
			return result, nil
		}
		return nil, fmt.Errorf("items2dict requires a list/slice, got %T", val)
	}
}

// TemplateItems2DictSimple 是 items2dict 在无参调用时的别名
func TemplateItems2DictSimple(val interface{}) (map[string]interface{}, error) {
	return TemplateItems2Dict(val)
}

// TemplateToYAML 将任意数据结构序列化为 YAML 字符串
func TemplateToYAML(val interface{}) (string, error) {
	if val == nil {
		return "null\n", nil
	}
	data, err := yaml.Marshal(val)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// TemplateToNiceYAML 将数据结构格式化为带指定缩进的 YAML 文本 (默认 indent: 2)
func TemplateToNiceYAML(val interface{}, args ...interface{}) (string, error) {
	if val == nil {
		return "null\n", nil
	}
	indent := 2
	if len(args) > 0 {
		switch v := args[0].(type) {
		case int:
			if v > 0 {
				indent = v
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				indent = n
			}
		}
	}
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(val); err != nil {
		return "", err
	}
	_ = enc.Close()
	return buf.String(), nil
}

// TemplateFromYAML 将 YAML 字符串反序列化为数据结构
func TemplateFromYAML(val interface{}) (interface{}, error) {
	if val == nil {
		return nil, nil
	}
	var raw []byte
	switch v := val.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		raw = []byte(fmt.Sprintf("%v", val))
	}
	var decoded interface{}
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// ExecuteLookup 统一执行 Ansible lookup 插件（env, file, pipe, template, password, first_found, url），
// 支持传入 baseDir 解析相对文件路径，实现 Playbook 表达式与 Jinja2 模板渲染的统一复用。
func ExecuteLookup(baseDir string, pluginName string, args ...interface{}) (interface{}, error) {
	return ExecuteLookupWithVars(baseDir, pluginName, nil, args...)
}

// ExecuteLookupWithVars 在 ExecuteLookup 基础上支持注入上下文变量字典（用于 template 等插件渲染）。
func ExecuteLookupWithVars(baseDir string, pluginName string, vars map[string]interface{}, args ...interface{}) (interface{}, error) {
	if len(args) == 0 {
		return "", nil
	}
	key := fmt.Sprintf("%v", args[0])

	resolvePath := func(p string) string {
		p = util.ExpandPath(p)
		if !filepath.IsAbs(p) && baseDir != "" {
			direct := filepath.Join(baseDir, p)
			if _, err := os.Stat(direct); err == nil {
				return direct
			}
			for _, sub := range []string{"files", "templates"} {
				subPath := filepath.Join(baseDir, sub, p)
				if _, err := os.Stat(subPath); err == nil {
					return subPath
				}
			}
			return direct
		}
		return p
	}

	switch strings.ToLower(strings.TrimSpace(pluginName)) {
	case "env":
		return os.Getenv(key), nil
	case "file":
		path := resolvePath(key)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("lookup 'file' failed to read %s: %w", path, err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	case "pipe":
		output, err := exec.Command("/bin/sh", "-c", key).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("lookup 'pipe' failed to execute %q: %v (%s)", key, err, strings.TrimSpace(string(output)))
		}
		return strings.TrimRight(string(output), "\r\n"), nil
	case "template":
		path := resolvePath(key)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("lookup 'template' failed to read %s: %w", path, err)
		}
		varsMap := make(map[string]interface{})
		for k, v := range vars {
			varsMap[k] = v
		}
		if len(args) > 1 {
			if vm, ok := args[1].(map[string]interface{}); ok {
				for k, v := range vm {
					varsMap[k] = v
				}
			}
		}
		rendered, err := RenderTemplateString(string(data), varsMap)
		if err != nil {
			return "", fmt.Errorf("lookup 'template' failed to render %s: %w", path, err)
		}
		return strings.TrimRight(rendered, "\r\n"), nil
	case "password":
		parts := strings.Fields(key)
		if len(parts) == 0 {
			return "", nil
		}
		path := resolvePath(parts[0])
		if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			return strings.TrimRight(string(data), "\r\n"), nil
		}
		length := 16
		chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		for _, param := range parts[1:] {
			if strings.HasPrefix(param, "length=") {
				if l, err := strconv.Atoi(param[len("length="):]); err == nil && l > 0 {
					length = l
				}
			} else if strings.HasPrefix(param, "chars=") {
				charsVal := param[len("chars="):]
				customChars := ""
				for _, cName := range strings.Split(charsVal, ",") {
					switch strings.TrimSpace(cName) {
					case "ascii_letters":
						customChars += "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
					case "ascii_lowercase":
						customChars += "abcdefghijklmnopqrstuvwxyz"
					case "ascii_uppercase":
						customChars += "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
					case "digits":
						customChars += "0123456789"
					case "punctuation":
						customChars += "!@#$%^&*()-_=+"
					default:
						customChars += cName
					}
				}
				if len(customChars) > 0 {
					chars = customChars
				}
			}
		}
		genPass := generateRandomLookupPassword(length, chars)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err == nil {
			_ = os.WriteFile(path, []byte(genPass+"\n"), 0600)
		}
		return genPass, nil
	case "first_found":
		candidates := strings.Split(key, ",")
		for _, c := range candidates {
			candidate := strings.TrimSpace(c)
			if candidate == "" {
				continue
			}
			p := resolvePath(candidate)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		if len(args) > 1 {
			for _, arg := range args[1:] {
				candidate := fmt.Sprintf("%v", arg)
				p := resolvePath(candidate)
				if _, err := os.Stat(p); err == nil {
					return p, nil
				}
			}
		}
		return "", nil
	case "url":
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(key)
		if err != nil {
			return "", nil
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", nil
		}
		return strings.TrimRight(string(body), "\r\n"), nil
	case "vars", "varnames":
		if vars != nil {
			if v, ok := vars[key]; ok {
				return v, nil
			}
			if strings.Contains(key, ".") {
				parts := strings.Split(key, ".")
				var cur interface{} = vars
				found := true
				for _, part := range parts {
					if m, ok := cur.(map[string]interface{}); ok {
						if next, exists := m[part]; exists {
							cur = next
						} else {
							found = false
							break
						}
					} else {
						found = false
						break
					}
				}
				if found {
					return cur, nil
				}
			}
		}
		if len(args) > 1 {
			return args[1], nil
		}
		return nil, fmt.Errorf("variable %q not found in context", key)
	case "lines":
		output, err := exec.Command("/bin/sh", "-c", key).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("lookup 'lines' failed to execute %q: %v (%s)", key, err, strings.TrimSpace(string(output)))
		}
		trimmed := strings.TrimRight(string(output), "\r\n")
		if trimmed == "" {
			return []string{}, nil
		}
		return util.SplitLines(trimmed), nil
	case "sequence":
		return executeSequenceLookup(key, args)
	case "random_choice":
		var candidates []interface{}
		if len(args) > 1 {
			candidates = append(candidates, args...)
		} else {
			switch v := args[0].(type) {
			case []interface{}:
				candidates = append(candidates, v...)
			case []string:
				for _, item := range v {
					candidates = append(candidates, item)
				}
			default:
				strVal := strings.TrimSpace(fmt.Sprintf("%v", v))
				if vars != nil {
					if val, ok := vars[strVal]; ok {
						if list, ok := val.([]interface{}); ok {
							candidates = append(candidates, list...)
						} else if list, ok := val.([]string); ok {
							for _, it := range list {
								candidates = append(candidates, it)
							}
						}
					}
				}
				if len(candidates) == 0 {
					clean := strings.Trim(strVal, "[]")
					if strings.Contains(clean, ",") {
						for _, item := range strings.Split(clean, ",") {
							if s := strings.Trim(strings.TrimSpace(item), "\"'"); s != "" {
								candidates = append(candidates, s)
							}
						}
					} else if clean != "" {
						candidates = append(candidates, clean)
					}
				}
			}
		}
		if len(candidates) == 0 {
			return "", nil
		}
		bigN, err := crand.Int(crand.Reader, big.NewInt(int64(len(candidates))))
		if err != nil {
			return candidates[0], nil
		}
		return candidates[bigN.Int64()], nil
	case "dict":
		var targetMap map[string]interface{}
		switch v := args[0].(type) {
		case map[string]interface{}:
			targetMap = v
		case map[string]string:
			targetMap = make(map[string]interface{}, len(v))
			for mk, mv := range v {
				targetMap[mk] = mv
			}
		default:
			strKey := strings.TrimSpace(fmt.Sprintf("%v", v))
			if vars != nil {
				if vm, ok := vars[strKey].(map[string]interface{}); ok {
					targetMap = vm
				} else if vm, ok := vars[strKey].(map[string]string); ok {
					targetMap = make(map[string]interface{}, len(vm))
					for mk, mv := range vm {
						targetMap[mk] = mv
					}
				}
			}
			if targetMap == nil && strings.HasPrefix(strKey, "{") {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(strKey), &parsed); err == nil {
					targetMap = parsed
				}
			}
		}
		if targetMap == nil {
			return []map[string]interface{}{}, nil
		}
		keys := make([]string, 0, len(targetMap))
		for k := range targetMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		result := make([]map[string]interface{}, 0, len(keys))
		for _, k := range keys {
			result = append(result, map[string]interface{}{
				"key":   k,
				"value": targetMap[k],
			})
		}
		return result, nil
	default:
		return key, nil
	}
}

func executeSequenceLookup(key string, args []interface{}) ([]string, error) {
	full := key
	for _, a := range args[1:] {
		full += " " + fmt.Sprintf("%v", a)
	}
	pairs := strings.Fields(full)
	var (
		start  = 1
		end    = -1
		count  = -1
		stride = 1
		format = "%d"
	)

	for _, p := range pairs {
		if strings.Contains(p, "=") {
			kv := strings.SplitN(p, "=", 2)
			k := strings.ToLower(strings.TrimSpace(kv[0]))
			v := strings.TrimSpace(kv[1])
			switch k {
			case "start":
				if s, err := strconv.Atoi(v); err == nil {
					start = s
				}
			case "end":
				if e, err := strconv.Atoi(v); err == nil {
					end = e
				}
			case "count":
				if c, err := strconv.Atoi(v); err == nil {
					count = c
				}
			case "stride":
				if st, err := strconv.Atoi(v); err == nil && st != 0 {
					stride = st
				}
			case "format":
				format = v
			}
		} else if c, err := strconv.Atoi(p); err == nil && count == -1 && end == -1 {
			count = c
		}
	}

	if count > 0 && end == -1 {
		end = start + (count-1)*stride
	}
	if end == -1 {
		end = start
	}

	var result []string
	if stride > 0 {
		for i := start; i <= end; i += stride {
			result = append(result, fmt.Sprintf(format, i))
		}
	} else if stride < 0 {
		for i := start; i >= end; i += stride {
			result = append(result, fmt.Sprintf(format, i))
		}
	}
	return result, nil
}

// TemplateLookup 实现了与 Playbook 表达式一致的 lookup 插件函数，可在 Jinja2 模板与表达式中无缝调用
func TemplateLookup(pluginName string, args ...interface{}) (interface{}, error) {
	return ExecuteLookup("", pluginName, args...)
}

func generateRandomLookupPassword(length int, chars string) string {
	if length <= 0 {
		length = 16
	}
	if len(chars) == 0 {
		chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	}
	runes := []rune(chars)
	result := make([]rune, length)
	for i := 0; i < length; i++ {
		n, err := crand.Int(crand.Reader, big.NewInt(int64(len(runes))))
		if err != nil {
			result[i] = runes[i%len(runes)]
		} else {
			result[i] = runes[n.Int64()]
		}
	}
	return string(result)
}

// strftimeToGoLayout converts common Python strftime formats into Go reference time format.
func strftimeToGoLayout(format string) string {
	r := strings.NewReplacer(
		"%Y", "2006",
		"%y", "06",
		"%m", "01",
		"%d", "02",
		"%H", "15",
		"%I", "03",
		"%p", "PM",
		"%M", "04",
		"%S", "05",
		"%f", "000000",
		"%z", "-0700",
		"%Z", "MST",
		"%b", "Jan",
		"%B", "January",
		"%a", "Mon",
		"%A", "Monday",
	)
	return r.Replace(format)
}

// TemplateToDatetime converts a date string into a time.Time or RFC3339 formatted string.
func TemplateToDatetime(args ...interface{}) interface{} {
	if len(args) == 0 || args[0] == nil {
		return time.Now().Format(time.RFC3339)
	}
	valStr := strings.TrimSpace(fmt.Sprintf("%v", args[0]))
	if valStr == "" {
		return time.Now().Format(time.RFC3339)
	}
	format := "%Y-%m-%d %H:%M:%S"
	if len(args) > 1 && args[1] != nil {
		f := strings.TrimSpace(fmt.Sprintf("%v", args[1]))
		if f != "" {
			format = f
		}
	}
	goLayout := strftimeToGoLayout(format)
	if t, err := time.Parse(goLayout, valStr); err == nil {
		return t.Format(time.RFC3339)
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, valStr); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return valStr
}

// TemplateStrftime formats a time or current time according to format string.
func TemplateStrftime(args ...interface{}) string {
	format := "%Y-%m-%d %H:%M:%S"
	var t time.Time = time.Now()

	if len(args) == 1 {
		s := fmt.Sprintf("%v", args[0])
		if strings.Contains(s, "%") {
			format = s
		} else if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			t = parsed
		} else if parsed, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
			t = parsed
		} else if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
			t = time.Unix(sec, 0)
		}
	} else if len(args) >= 2 {
		s := fmt.Sprintf("%v", args[0])
		format = fmt.Sprintf("%v", args[1])
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			t = parsed
		} else if parsed, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
			t = parsed
		} else if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
			t = time.Unix(sec, 0)
		}
	}
	goLayout := strftimeToGoLayout(format)
	return t.Format(goLayout)
}

// TemplateUrlencode escapes URL query parameters.
func TemplateUrlencode(value interface{}) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return url.QueryEscape(v)
	case map[string]interface{}:
		vals := url.Values{}
		var keys []string
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			vals.Set(k, fmt.Sprintf("%v", v[k]))
		}
		return vals.Encode()
	case map[string]string:
		vals := url.Values{}
		var keys []string
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			vals.Set(k, v[k])
		}
		return vals.Encode()
	default:
		return url.QueryEscape(fmt.Sprintf("%v", value))
	}
}

// TemplateTypeDebug returns the pythonic name of the underlying type.
func TemplateTypeDebug(value interface{}) string {
	if value == nil {
		return "NoneType"
	}
	rt := reflect.TypeOf(value)
	switch rt.Kind() {
	case reflect.String:
		return "str"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Bool:
		return "bool"
	case reflect.Slice, reflect.Array:
		return "list"
	case reflect.Map, reflect.Struct:
		return "dict"
	default:
		return rt.Kind().String()
	}
}
