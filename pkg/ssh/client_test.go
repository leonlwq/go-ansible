package ssh

import (
	"testing"
	"time"

	"go-ansible/pkg/inventory"
)

func TestParseSSHCommonArgs(t *testing.T) {
	opts, err := parseSSHCommonArgs("-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15")
	if err != nil {
		t.Fatalf("parseSSHCommonArgs() error = %v", err)
	}

	if opts.StrictHostKeyChecking == nil || *opts.StrictHostKeyChecking {
		t.Fatalf("expected StrictHostKeyChecking=false, got %#v", opts.StrictHostKeyChecking)
	}
	if opts.UserKnownHostsFile != "/dev/null" {
		t.Fatalf("expected UserKnownHostsFile=/dev/null, got %q", opts.UserKnownHostsFile)
	}
	if opts.ConnectTimeout != 15*time.Second {
		t.Fatalf("expected ConnectTimeout=15s, got %v", opts.ConnectTimeout)
	}
}

func TestResolveHostKeyCallbackStrictRequiresKnownHosts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := resolveHostKeyCallback("-o StrictHostKeyChecking=yes"); err == nil {
		t.Fatal("expected missing known_hosts to return error")
	}
}

func TestNewClientAppliesSSHCommonArgsTimeout(t *testing.T) {
	client, err := NewClient(&inventory.Host{
		Name:          "app01",
		Address:       "127.0.0.1",
		Port:          22,
		User:          "root",
		SSHCommonArgs: "-o StrictHostKeyChecking=no -o ConnectTimeout=5",
	}, DefaultConfig())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if client.config.Timeout != 5*time.Second {
		t.Fatalf("expected timeout 5s, got %v", client.config.Timeout)
	}
	if client.config.HostKeyCallback == nil {
		t.Fatal("expected HostKeyCallback to be configured")
	}
}

func TestProxyJumpParsing(t *testing.T) {
	opts, err := parseSSHCommonArgs("-o ProxyJump=bastion.example.com:2222")
	if err != nil {
		t.Fatal(err)
	}
	if opts.ProxyJump != "bastion.example.com:2222" {
		t.Fatalf("expected bastion.example.com:2222, got %q", opts.ProxyJump)
	}

	client, err := NewClient(&inventory.Host{
		Name:          "internal-srv",
		Address:       "10.0.1.5",
		Port:          22,
		User:          "app",
		SSHCommonArgs: "-o StrictHostKeyChecking=no -o ProxyJump=jumpuser@jumpbox",
	}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if client.proxyJump != "jumpuser@jumpbox" {
		t.Fatalf("expected jumpuser@jumpbox, got %q", client.proxyJump)
	}
}
