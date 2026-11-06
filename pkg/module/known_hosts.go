package module

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// KnownHostsModule 实现了 ansible.builtin.known_hosts 核心功能
type KnownHostsModule struct{}

func (m *KnownHostsModule) Name() string {
	return "known_hosts"
}

func (m *KnownHostsModule) Validate(params map[string]interface{}) error {
	name := strings.TrimSpace(GetParamString(params, "name", ""))
	if name == "" {
		return fmt.Errorf("missing required parameter: name")
	}

	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "present")))
	if state == "" {
		state = "present"
	}
	if state != "present" && state != "absent" {
		return fmt.Errorf("unsupported state: %s (must be present or absent)", state)
	}

	if state == "present" {
		key := strings.TrimSpace(GetParamString(params, "key", ""))
		if key == "" {
			return fmt.Errorf("missing required parameter: key (required when state=present)")
		}
	}

	return nil
}

type knownHostEntry struct {
	raw      string
	marker   string // e.g. "@cert-authority" or "@revoked"
	host     string // host pattern or hashed host |1|...
	keyType  string // e.g. "ssh-ed25519"
	keyBlob  string // base64 encoded public key
	comment  string // optional comment
	isEntry  bool
	isHashed bool
}

func parseKnownHostLine(line string) knownHostEntry {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return knownHostEntry{raw: line, isEntry: false}
	}

	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return knownHostEntry{raw: line, isEntry: false}
	}

	marker := ""
	idx := 0
	if strings.HasPrefix(fields[0], "@") {
		marker = fields[0]
		idx = 1
	}

	if len(fields)-idx < 2 {
		return knownHostEntry{raw: line, isEntry: false}
	}

	hostField := fields[idx]
	keyTypeField := fields[idx+1]
	keyBlobField := ""
	comment := ""

	if len(fields)-idx >= 3 {
		keyBlobField = fields[idx+2]
		if len(fields)-idx > 3 {
			comment = strings.Join(fields[idx+3:], " ")
		}
	}

	isHashed := strings.HasPrefix(hostField, "|1|")

	return knownHostEntry{
		raw:      trimmed,
		marker:   marker,
		host:     hostField,
		keyType:  keyTypeField,
		keyBlob:  keyBlobField,
		comment:  comment,
		isEntry:  true,
		isHashed: isHashed,
	}
}

func (e knownHostEntry) Format() string {
	if !e.isEntry {
		return e.raw
	}
	var sb strings.Builder
	if e.marker != "" {
		sb.WriteString(e.marker)
		sb.WriteString(" ")
	}
	sb.WriteString(e.host)
	sb.WriteString(" ")
	sb.WriteString(e.keyType)
	sb.WriteString(" ")
	sb.WriteString(e.keyBlob)
	if e.comment != "" {
		sb.WriteString(" ")
		sb.WriteString(e.comment)
	}
	return sb.String()
}

func matchHashedHost(hashed string, hostname string) bool {
	parts := strings.Split(hashed, "|")
	if len(parts) != 4 || parts[1] != "1" {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expectedHash, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}

	targets := strings.Split(hostname, ",")
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		// OpenSSH standard normalized address comparison
		mac := hmac.New(sha1.New, salt)
		mac.Write([]byte(knownhosts.Normalize(target)))
		if hmac.Equal(mac.Sum(nil), expectedHash) {
			return true
		}
		// Also raw hostname comparison
		mac2 := hmac.New(sha1.New, salt)
		mac2.Write([]byte(target))
		if hmac.Equal(mac2.Sum(nil), expectedHash) {
			return true
		}
	}
	return false
}

func matchHostPattern(hostInLine, targetName string) bool {
	if strings.HasPrefix(hostInLine, "|1|") {
		return matchHashedHost(hostInLine, targetName)
	}

	targetNorm := knownhosts.Normalize(targetName)
	targetHosts := strings.Split(targetName, ",")

	lineHosts := strings.Split(hostInLine, ",")
	for _, lh := range lineHosts {
		lh = strings.TrimSpace(lh)
		if lh == "" {
			continue
		}
		if lh == targetName || knownhosts.Normalize(lh) == targetNorm {
			return true
		}
		for _, th := range targetHosts {
			th = strings.TrimSpace(th)
			if th != "" && (lh == th || knownhosts.Normalize(lh) == knownhosts.Normalize(th)) {
				return true
			}
		}
	}
	return false
}

func parseKeyParam(name, rawKey string, hashHost bool) (marker string, host string, keyType string, keyBlob string, comment string, err error) {
	rawKey = strings.TrimSpace(rawKey)
	fields := strings.Fields(rawKey)
	if len(fields) == 0 {
		return "", "", "", "", "", fmt.Errorf("empty key parameter")
	}

	idx := 0
	if strings.HasPrefix(fields[0], "@") {
		marker = fields[0]
		idx = 1
	}

	remaining := fields[idx:]
	if len(remaining) == 0 {
		return "", "", "", "", "", fmt.Errorf("invalid key parameter: %s", rawKey)
	}

	if isSSHKeyType(remaining[0]) {
		// e.g. "ssh-ed25519 AAAAC3... [comment]"
		keyType = remaining[0]
		if len(remaining) < 2 {
			return "", "", "", "", "", fmt.Errorf("missing key blob in key parameter: %s", rawKey)
		}
		keyBlob = remaining[1]
		if len(remaining) > 2 {
			comment = strings.Join(remaining[2:], " ")
		}
	} else {
		// e.g. "github.com ssh-ed25519 AAAAC3... [comment]"
		if len(remaining) < 3 {
			return "", "", "", "", "", fmt.Errorf("invalid key format: expected at least host, key-type, and key-blob")
		}
		keyType = remaining[1]
		keyBlob = remaining[2]
		if len(remaining) > 3 {
			comment = strings.Join(remaining[3:], " ")
		}
	}

	if hashHost {
		host = knownhosts.HashHostname(name)
	} else {
		host = name
	}

	return marker, host, keyType, keyBlob, comment, nil
}

func resolveRemoteKnownHostsPath(client ssh.Connection, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "~/.ssh/known_hosts"
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		homeDir := ""
		res, err := client.Execute("echo $HOME")
		if err == nil && res != nil && strings.TrimSpace(res.Stdout) != "" {
			homeDir = strings.TrimSpace(res.Stdout)
		}
		if homeDir == "" {
			if h, err := os.UserHomeDir(); err == nil && h != "" {
				homeDir = h
			} else {
				homeDir = "/root"
			}
		}
		if p == "~" {
			return homeDir
		}
		return filepath.Join(homeDir, p[2:])
	}
	return p
}

func (m *KnownHostsModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := strings.TrimSpace(GetParamString(params, "name", ""))
	rawKey := strings.TrimSpace(GetParamString(params, "key", ""))
	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "present")))
	if state == "" {
		state = "present"
	}
	pathParam := strings.TrimSpace(GetParamString(params, "path", ""))
	hashHost := GetParamBool(params, "hash_host", false)

	checkMode := IsCheckMode(params)
	diffMode := IsDiffMode(params)

	targetFile := resolveRemoteKnownHostsPath(client, pathParam)
	targetDir := filepath.Dir(targetFile)

	// 1. 读取现有 known_hosts 内容
	existingRaw := ""
	readRes, err := client.Execute(fmt.Sprintf("cat %s 2>/dev/null || true", util.Quote(targetFile)))
	if err == nil && readRes != nil {
		existingRaw = readRes.Stdout
	}

	var existingLines []string
	if strings.TrimSpace(existingRaw) != "" {
		existingLines = strings.Split(strings.TrimRight(existingRaw, "\r\n"), "\n")
	}

	var existingEntries []knownHostEntry
	for _, l := range existingLines {
		existingEntries = append(existingEntries, parseKnownHostLine(l))
	}

	var newLines []string
	changed := false

	switch state {
	case "absent":
		targetKeyBlob := ""
		if rawKey != "" {
			_, _, _, kb, _, err := parseKeyParam(name, rawKey, false)
			if err == nil {
				targetKeyBlob = kb
			}
		}

		for _, ee := range existingEntries {
			if !ee.isEntry {
				newLines = append(newLines, ee.raw)
				continue
			}

			if matchHostPattern(ee.host, name) {
				if targetKeyBlob != "" {
					if ee.keyBlob == targetKeyBlob {
						changed = true
						continue // delete this matching key
					}
					// different key for this host, keep it
					newLines = append(newLines, ee.Format())
				} else {
					// delete all entries matching this host
					changed = true
					continue
				}
			} else {
				newLines = append(newLines, ee.Format())
			}
		}

	case "present":
		marker, hostFormatted, keyType, keyBlob, comment, err := parseKeyParam(name, rawKey, hashHost)
		if err != nil {
			return nil, err
		}

		targetEntry := knownHostEntry{
			raw:      "",
			marker:   marker,
			host:     hostFormatted,
			keyType:  keyType,
			keyBlob:  keyBlob,
			comment:  comment,
			isEntry:  true,
			isHashed: hashHost,
		}

		found := false
		for _, ee := range existingEntries {
			if !ee.isEntry {
				newLines = append(newLines, ee.raw)
				continue
			}

			if matchHostPattern(ee.host, name) {
				if ee.keyType == keyType {
					found = true
					// Check if exact key blob matches
					if ee.keyBlob == keyBlob {
						// Check hash requirements
						if hashHost && !ee.isHashed {
							// Needs hashing update
							changed = true
							newLines = append(newLines, targetEntry.Format())
						} else {
							// Already matches
							newLines = append(newLines, ee.Format())
						}
					} else {
						// Outdated key for this host and keyType, update it
						changed = true
						newLines = append(newLines, targetEntry.Format())
					}
				} else {
					// Different keyType (e.g. ssh-rsa vs ssh-ed25519), keep existing
					newLines = append(newLines, ee.Format())
				}
			} else {
				newLines = append(newLines, ee.Format())
			}
		}

		if !found {
			changed = true
			newLines = append(newLines, targetEntry.Format())
		}

	default:
		return nil, fmt.Errorf("unsupported state: %s", state)
	}

	newContent := ""
	if len(newLines) > 0 {
		newContent = strings.Join(newLines, "\n") + "\n"
	}
	oldContent := ""
	if len(existingLines) > 0 {
		oldContent = strings.Join(existingLines, "\n") + "\n"
	}

	if oldContent == newContent {
		changed = false
	}

	diffText := ""
	if diffMode && changed {
		diffText = buildTextDiff(targetFile, oldContent, newContent)
	}

	if checkMode {
		return &Result{
			Changed: changed,
			Diff:    diffText,
			Message: "check mode: known_hosts would be updated",
		}, nil
	}

	if changed {
		client.Execute(fmt.Sprintf("mkdir -p %s && chmod 700 %s", util.Quote(targetDir), util.Quote(targetDir)))

		tmpFile := fmt.Sprintf("%s.tmp.%d", targetFile, time.Now().UnixNano())
		writeCmd := fmt.Sprintf("cat << 'EOF_ANSIBLE_KNOWN_HOSTS' > %s\n%sEOF_ANSIBLE_KNOWN_HOSTS\nchmod 600 %s && mv -f %s %s",
			util.Quote(tmpFile), newContent, util.Quote(tmpFile), util.Quote(tmpFile), util.Quote(targetFile))
		writeRes, err := client.Execute(writeCmd)
		if err != nil {
			return nil, fmt.Errorf("write known_hosts: %w", err)
		}
		if writeRes != nil && writeRes.ExitCode != 0 {
			return nil, fmt.Errorf("write known_hosts failed: %s", strings.TrimSpace(writeRes.Stderr))
		}
	}

	return &Result{
		Changed: changed,
		Diff:    diffText,
		Message: "known_hosts updated successfully",
	}, nil
}
