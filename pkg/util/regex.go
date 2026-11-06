package util

import (
	"regexp"
	"sync"
)

var regexPool sync.Map // map[string]*regexp.Regexp

// GetRegex returns a compiled regular expression from the cache pool, or compiles and caches it.
func GetRegex(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexPool.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexPool.Store(pattern, re)
	return re, nil
}

// RegexPool returns the global regular expression cache pool.
func RegexPool() *sync.Map {
	return &regexPool
}

// ClearRegexPool flushes the compiled regular expression cache pool (useful for test isolation).
func ClearRegexPool() {
	regexPool.Range(func(key, _ interface{}) bool {
		regexPool.Delete(key)
		return true
	})
}
