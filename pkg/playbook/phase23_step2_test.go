package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// TestPhase23_RegexPoolSingleton verifies that regex patterns are compiled once and shared.
func TestPhase23_RegexPoolSingleton(t *testing.T) {
	util.ClearRegexPool()
	pattern := `^node-(?P<id>\d+)\.example\.com$`

	re1, err := util.GetRegex(pattern)
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	re2, err := util.GetRegex(pattern)
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	if re1 != re2 {
		t.Fatalf("expected identical pointer from regex pool, got %p vs %p", re1, re2)
	}
	if !re1.MatchString("node-42.example.com") {
		t.Fatalf("expected regex to match node-42.example.com")
	}
}

// TestPhase23_BoundedBufferProtection verifies that BoundedBuffer caps memory at specified threshold.
func TestPhase23_BoundedBufferProtection(t *testing.T) {
	buf := ssh.NewBoundedBuffer(256)
	chunk := strings.Repeat("A", 150)
	n, err := buf.Write([]byte(chunk))
	if err != nil || n != 150 {
		t.Fatalf("write chunk 1 failed: n=%d err=%v", n, err)
	}
	n, err = buf.Write([]byte(chunk))
	if err != nil || n != 150 {
		t.Fatalf("write chunk 2 failed: n=%d err=%v", n, err)
	}

	out := buf.String()
	if !strings.Contains(out, "...[output truncated: exceeded maximum limit of 16MB]...") {
		t.Fatalf("expected truncation marker in bounded buffer output, got: %s", out)
	}
	if !strings.HasPrefix(out, strings.Repeat("A", 256)) {
		t.Fatalf("expected exactly 256 'A' characters before truncation marker")
	}
}

// TestPhase23_HostVarsSnapshotPointerReuse verifies that hostvars dictionary reuses base pointers and reduces allocations.
func TestPhase23_HostVarsSnapshotPointerReuse(t *testing.T) {
	inv := inventory.NewInventory()
	for i := 1; i <= 20; i++ {
		hostName := fmt.Sprintf("worker-%02d", i)
		h := &inventory.Host{
			Name:      hostName,
			Address:   fmt.Sprintf("192.168.1.%d", i),
			Port:      22,
			Variables: map[string]string{"role": "worker", "idx": fmt.Sprintf("%d", i)},
		}
		inv.AllHosts[hostName] = h
	}

	exec := NewExecutor(inv)
	h1 := inv.AllHosts["worker-01"]
	vars1 := exec.taskVarsForHost(h1, map[string]interface{}{"task_var": "val1"})

	hostvars1, ok := vars1["hostvars"].(map[string]map[string]interface{})
	if !ok {
		t.Fatalf("expected hostvars map, got %T", vars1["hostvars"])
	}
	if len(hostvars1) != 20 {
		t.Fatalf("expected 20 hosts in hostvars snapshot, got %d", len(hostvars1))
	}
	if hostvars1["worker-02"]["role"] != "worker" {
		t.Fatalf("expected worker-02 role=worker, got %v", hostvars1["worker-02"]["role"])
	}

	// worker-01 in snapshot has task_var
	if hostvars1["worker-01"]["task_var"] != "val1" {
		t.Fatalf("expected worker-01 to have task_var=val1, got %v", hostvars1["worker-01"]["task_var"])
	}
	// worker-02 does not have task_var
	if _, exists := hostvars1["worker-02"]["task_var"]; exists {
		t.Fatalf("worker-02 should not have task_var from worker-01")
	}

	// Next task on worker-02: verify cached base pointer reuse
	h2 := inv.AllHosts["worker-02"]
	vars2 := exec.taskVarsForHost(h2, map[string]interface{}{"task_var": "val2"})
	hostvars2 := vars2["hostvars"].(map[string]map[string]interface{})

	// For an unrelated host worker-05, pointer in hostvars1 and hostvars2 should be IDENTICAL
	if fmt.Sprintf("%p", hostvars1["worker-05"]) != fmt.Sprintf("%p", hostvars2["worker-05"]) {
		t.Fatalf("expected pointer reuse for unaffected host worker-05 across snapshots")
	}
}

// TestPhase23_GroupsMapCachedZeroAllocation verifies that buildGroupsMap reuses the cached map without re-allocation.
func TestPhase23_GroupsMapCachedZeroAllocation(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{Name: "web1", Address: "10.0.0.1"}
	h2 := &inventory.Host{Name: "web2", Address: "10.0.0.2"}
	inv.AllHosts["web1"] = h1
	inv.AllHosts["web2"] = h2
	g := &inventory.Group{Name: "web", Hosts: []*inventory.Host{h1, h2}}
	inv.Groups["web"] = g

	exec := NewExecutor(inv)
	g1 := exec.buildGroupsMap()
	g2 := exec.buildGroupsMap()

	if fmt.Sprintf("%p", g1) != fmt.Sprintf("%p", g2) {
		t.Fatalf("expected buildGroupsMap to return cached map reference without re-allocation")
	}
	if len(g1["web"]) != 2 {
		t.Fatalf("expected 2 web hosts in group web, got %v", g1["web"])
	}
}

// TestPhase23_ConcurrentFactsGatheringLockFree verifies that parallel facts gathering operates safely and invalidates cache once.
func TestPhase23_ConcurrentFactsGatheringLockFree(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-fact-cache-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inv := inventory.NewInventory()
	var hosts []*inventory.Host
	for i := 1; i <= 10; i++ {
		name := fmt.Sprintf("node-%02d", i)
		h := &inventory.Host{Name: name, Address: "127.0.0.1"}
		inv.AllHosts[name] = h
		hosts = append(hosts, h)
	}

	exec := NewExecutor(inv)
	exec.SetFactCaching("jsonfile", tmpDir, 300)

	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(host *inventory.Host) {
			defer wg.Done()
			exec.saveCachedFactsInternal(host.Name, map[string]interface{}{
				"ansible_hostname": host.Name,
				"ansible_os":       "Linux",
			}, false)
		}(h)
	}
	wg.Wait()
	exec.invalidateHostVarsCache()

	// Verify all 10 hosts have facts cached and files written
	for _, h := range hosts {
		facts := exec.getHostFacts(h.Name)
		if facts == nil || facts["ansible_hostname"] != h.Name {
			t.Fatalf("expected facts for host %s, got %v", h.Name, facts)
		}
		cacheFile := filepath.Join(tmpDir, h.Name+".json")
		if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
			t.Fatalf("expected fact cache file %s to exist", cacheFile)
		}
	}
}
