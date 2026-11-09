package inventory

import (
	"sync"
	"testing"
)

// TestPhase36_ResolveHostVarsRawCacheAndInvalidation 验证 ResolveHostVarsRaw 缓存命中与失效
func TestPhase36_ResolveHostVarsRawCacheAndInvalidation(t *testing.T) {
	inv := NewInventory()
	h := &Host{
		Name:    "web1",
		Address: "192.168.1.10",
		Variables: map[string]string{
			"app": "nginx",
		},
	}
	inv.AllHosts[h.Name] = h

	g := &Group{
		Name:  "webservers",
		Hosts: []*Host{h},
		Variables: map[string]string{
			"port": "80",
		},
	}
	inv.Groups[g.Name] = g

	// 第一次解析：计算并存入 resolvedVarsRawCache
	vars1 := inv.ResolveHostVarsRaw(h)
	if vars1["app"] != "nginx" || vars1["port"] != "80" {
		t.Fatalf("unexpected initial vars: %#v", vars1)
	}

	// 缓存命中验证：直接从缓存中返回克隆副本
	vars2 := inv.ResolveHostVarsRaw(h)
	if vars2["app"] != "nginx" || vars2["port"] != "80" {
		t.Fatalf("unexpected cached vars: %#v", vars2)
	}

	// 设置主机原生复杂变量，验证缓存自动失效并生效
	inv.SetHostVar("web1", "extra_list", []string{"a", "b"})
	vars3 := inv.ResolveHostVarsRaw(h)
	if list, ok := vars3["extra_list"].([]string); !ok || len(list) != 2 {
		t.Fatalf("expected invalidated cache to load extra_list: %#v", vars3["extra_list"])
	}

	// 设置组原生复杂变量，验证缓存自动失效
	inv.SetGroupVar("webservers", "nested_map", map[string]int{"workers": 4})
	vars4 := inv.ResolveHostVarsRaw(h)
	if nMap, ok := vars4["nested_map"].(map[string]int); !ok || nMap["workers"] != 4 {
		t.Fatalf("expected invalidated cache to load nested_map: %#v", vars4["nested_map"])
	}

	// 并发读取安全性验证
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = inv.ResolveHostVarsRaw(h)
			}
		}()
	}
	wg.Wait()
}

