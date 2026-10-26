package inventory

import (
	"testing"
)

func TestImplicitLocalhost(t *testing.T) {
	inv := NewInventory()

	// 1. 验证空 Inventory 获取 localhost 会自动创建隐式 localhost
	lh, err := inv.GetHost("localhost")
	if err != nil {
		t.Fatalf("unexpected error getting localhost: %v", err)
	}
	if lh.Name != "localhost" {
		t.Errorf("expected host name 'localhost', got %s", lh.Name)
	}
	if lh.Variables["ansible_connection"] != "local" {
		t.Errorf("expected ansible_connection 'local', got %s", lh.Variables["ansible_connection"])
	}

	// 2. 验证通过 127.0.0.1 获取也能返回 localhost
	lh2, err := inv.GetHost("127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error getting 127.0.0.1: %v", err)
	}
	if lh2 != lh {
		t.Errorf("expected lh2 to be same instance as lh")
	}

	// 3. 验证 MatchHosts("localhost")
	matched, err := inv.MatchHosts("localhost")
	if err != nil {
		t.Fatalf("unexpected error matching localhost: %v", err)
	}
	if len(matched) != 1 || matched[0].Name != "localhost" {
		t.Errorf("expected 1 match for localhost, got %v", matched)
	}

	// 4. 全新空 inventory 下 MatchHosts("localhost") 能够自动 EnsureLocalhost
	freshInv := NewInventory()
	freshMatched, err := freshInv.MatchHosts("localhost")
	if err != nil {
		t.Fatalf("unexpected error matching localhost on fresh inventory: %v", err)
	}
	if len(freshMatched) != 1 || freshMatched[0].Name != "localhost" {
		t.Errorf("expected 1 match for localhost, got %v", freshMatched)
	}

	// 5. 验证不存在的主机报错
	_, err = inv.GetHost("non_existent_host")
	if err == nil {
		t.Errorf("expected error for non_existent_host, got nil")
	}
}
