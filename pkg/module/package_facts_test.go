package module

import (
	"testing"

	"go-ansible/pkg/ssh"
)

func TestPackageFactsParsing(t *testing.T) {
	// 1. Test APT output parsing
	aptOutput := `MGR:apt
bash	5.1-6ubuntu1	amd64
curl	7.81.0-1ubuntu1.16	amd64
libc6	2.35-0ubuntu3.6	amd64
libc6	2.35-0ubuntu3.6	i386
`
	pkgs := parsePackageFactsOutput(aptOutput)
	if len(pkgs) != 3 {
		t.Fatalf("expected 3 distinct package names, got %d", len(pkgs))
	}

	curlEntries, ok := pkgs["curl"].([]interface{})
	if !ok || len(curlEntries) != 1 {
		t.Fatalf("expected 1 entry for curl, got %v", pkgs["curl"])
	}
	curlMap := curlEntries[0].(map[string]interface{})
	if curlMap["version"] != "7.81.0-1ubuntu1.16" || curlMap["arch"] != "amd64" || curlMap["source"] != "apt" {
		t.Errorf("unexpected curl details: %#v", curlMap)
	}

	// Verify multi-arch package libc6 has 2 versions
	libcEntries, ok := pkgs["libc6"].([]interface{})
	if !ok || len(libcEntries) != 2 {
		t.Fatalf("expected 2 entries for libc6, got %v", pkgs["libc6"])
	}

	// 2. Test RPM output parsing
	rpmOutput := `MGR:rpm
openssh-server	8.7p1-38.el9	x86_64
glibc	2.34-100.el9	x86_64
`
	rpmPkgs := parsePackageFactsOutput(rpmOutput)
	sshEntries, ok := rpmPkgs["openssh-server"].([]interface{})
	if !ok || len(sshEntries) != 1 {
		t.Fatalf("expected openssh-server in rpm packages")
	}
	sshMap := sshEntries[0].(map[string]interface{})
	if sshMap["version"] != "8.7p1-38.el9" || sshMap["source"] != "rpm" {
		t.Errorf("unexpected ssh details: %#v", sshMap)
	}

	// 3. Test APK output parsing
	apkOutput := `MGR:apk
alpine-keys-2.4-r1
busybox-1.35.0-r29
ca-certificates-bundle-20230506-r0
`
	apkPkgs := parsePackageFactsOutput(apkOutput)
	bbEntries, ok := apkPkgs["busybox"].([]interface{})
	if !ok || len(bbEntries) != 1 {
		t.Fatalf("expected busybox in apk packages, got %#v", apkPkgs)
	}
	bbMap := bbEntries[0].(map[string]interface{})
	if bbMap["version"] != "1.35.0-r29" || bbMap["source"] != "apk" {
		t.Errorf("unexpected busybox version: %#v", bbMap)
	}

	// 4. Test Pacman / Brew output parsing
	brewOutput := `MGR:brew
git 2.44.0
go 1.22.1
`
	brewPkgs := parsePackageFactsOutput(brewOutput)
	gitEntries, ok := brewPkgs["git"].([]interface{})
	if !ok || len(gitEntries) != 1 {
		t.Fatalf("expected git in brew packages")
	}
	gitMap := gitEntries[0].(map[string]interface{})
	if gitMap["version"] != "2.44.0" || gitMap["source"] != "brew" {
		t.Errorf("unexpected git brew details: %#v", gitMap)
	}
}

func TestPackageFactsModuleExecution(t *testing.T) {
	client := &ssh.LocalConnection{}
	mod := &PackageFactsModule{}

	// Validation
	if err := mod.Validate(map[string]interface{}{"manager": "invalid_mgr"}); err == nil {
		t.Fatalf("expected error on invalid manager")
	}
	if err := mod.Validate(map[string]interface{}{"manager": "auto"}); err != nil {
		t.Fatalf("validate auto error: %v", err)
	}

	res, err := mod.Execute(client, map[string]interface{}{
		"manager":      "auto",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Changed {
		t.Errorf("package_facts must have changed=false")
	}
	if res.Data == nil {
		t.Fatalf("expected non-nil Data")
	}
	af, ok := res.Data["ansible_facts"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected ansible_facts in Data")
	}
	if _, ok := af["packages"].(map[string]interface{}); !ok {
		t.Fatalf("expected packages map in ansible_facts")
	}
}
