package playbook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"go-ansible/pkg/inventory"
)

func (e *Executor) SetFactCaching(plugin, connection string, timeout int) {
	e.factCaching = strings.ToLower(strings.TrimSpace(plugin))
	e.factCachingConnection = strings.TrimSpace(connection)
	e.factCachingTimeout = timeout
}

func (e *Executor) FlushCache() {
	e.mu.Lock()
	e.facts = make(map[string]map[string]interface{})
	e.factCacheTimestamps = make(map[string]time.Time)
	e.mu.Unlock()
	e.invalidateHostVarsCache()

	if e.factCaching == "jsonfile" && e.factCachingConnection != "" {
		entries, err := os.ReadDir(e.factCachingConnection)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
					_ = os.Remove(filepath.Join(e.factCachingConnection, entry.Name()))
				}
			}
		}
	}
}

func (e *Executor) getCachedFacts(hostName string) (map[string]interface{}, bool) {
	e.mu.RLock()
	mode := e.factCaching
	conn := e.factCachingConnection
	timeout := e.factCachingTimeout
	if mode == "memory" {
		if cached, ok := e.facts[hostName]; ok && cached != nil {
			if timeout > 0 {
				if ts, ok := e.factCacheTimestamps[hostName]; ok {
					if time.Since(ts) > time.Duration(timeout)*time.Second {
						e.mu.RUnlock()
						return nil, false
					}
				}
			}
			cloned := make(map[string]interface{}, len(cached))
			for k, v := range cached {
				cloned[k] = v
			}
			e.mu.RUnlock()
			return cloned, true
		}
	}
	e.mu.RUnlock()

	if mode == "jsonfile" {
		if conn == "" {
			return nil, false
		}
		cacheFile := filepath.Join(conn, hostName+".json")
		info, err := os.Stat(cacheFile)
		if err != nil {
			return nil, false
		}
		if timeout > 0 && time.Since(info.ModTime()) > time.Duration(timeout)*time.Second {
			return nil, false
		}
		data, err := os.ReadFile(cacheFile)
		if err != nil {
			return nil, false
		}
		var facts map[string]interface{}
		if err := json.Unmarshal(data, &facts); err == nil && facts != nil {
			return facts, true
		}
	}
	return nil, false
}

func (e *Executor) saveCachedFacts(hostName string, facts map[string]interface{}) {
	e.saveCachedFactsInternal(hostName, facts, true)
}

func (e *Executor) saveCachedFactsInternal(hostName string, facts map[string]interface{}, invalidateNow bool) {
	e.mu.Lock()
	e.facts[hostName] = facts
	if e.factCacheTimestamps == nil {
		e.factCacheTimestamps = make(map[string]time.Time)
	}
	e.factCacheTimestamps[hostName] = time.Now()
	e.mu.Unlock()

	if invalidateNow {
		e.invalidateHostVarsCache(hostName)
	}

	if e.factCaching == "jsonfile" && e.factCachingConnection != "" {
		cacheFile := filepath.Join(e.factCachingConnection, hostName+".json")
		data, err := json.MarshalIndent(facts, "", "  ")
		if err == nil {
			_ = os.WriteFile(cacheFile, data, 0644)
		}
	}
}

func (e *Executor) getHostFacts(hostName string) map[string]interface{} {
	e.mu.RLock()
	facts, ok := e.facts[hostName]
	if ok && facts != nil {
		cloned := make(map[string]interface{}, len(facts))
		for k, v := range facts {
			cloned[k] = v
		}
		e.mu.RUnlock()
		return cloned
	}
	e.mu.RUnlock()

	if cached, found := e.getCachedFacts(hostName); found {
		e.mu.Lock()
		if e.facts[hostName] == nil {
			e.facts[hostName] = make(map[string]interface{})
		}
		for k, v := range cached {
			e.facts[hostName][k] = v
		}
		cloned := make(map[string]interface{}, len(e.facts[hostName]))
		for k, v := range e.facts[hostName] {
			cloned[k] = v
		}
		e.mu.Unlock()
		return cloned
	}
	return nil
}

// gatherFacts 收集主机信息
func (e *Executor) gatherFacts(hosts []*inventory.Host) {
	e.gatherFactsForPlay(nil, hosts)
}

// gatherFactsForPlay 收集指定 play 上下文的主机信息
func (e *Executor) gatherFactsForPlay(play *Play, hosts []*inventory.Host) {
	mod, err := e.registry.Get("setup")
	if err != nil {
		return
	}

	if e.factCaching == "jsonfile" && e.factCachingConnection != "" {
		_ = os.MkdirAll(e.factCachingConnection, 0755)
	}

	forks := e.GetForks()
	if forks <= 0 {
		forks = 5
	}
	sem := make(chan struct{}, forks)
	var wg sync.WaitGroup

	for _, host := range hosts {
		wg.Add(1)
		sem <- struct{}{}
		go func(h *inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			if cached, found := e.getCachedFacts(h.Name); found {
				e.mu.Lock()
				e.facts[h.Name] = cached
				e.mu.Unlock()
				return
			}

			client := e.getOrCreateClient(h)
			if client == nil {
				return
			}

			var setupParams map[string]interface{}
			if play != nil && (len(play.GatherSubset) > 0 || play.GatherTimeout > 0) {
				setupParams = make(map[string]interface{})
				if len(play.GatherSubset) > 0 {
					setupParams["gather_subset"] = play.GatherSubset
				}
				if play.GatherTimeout > 0 {
					setupParams["gather_timeout"] = play.GatherTimeout
				}
			}

			result, err := mod.Execute(client, setupParams)
			if err == nil && result != nil {
				parsed := e.parseFacts(result.Stdout)
				e.saveCachedFactsInternal(h.Name, parsed, false)
			}
		}(host)
	}
	wg.Wait()
	e.invalidateHostVarsCache()
}

// parseFacts 解析 facts 输出
func parseFacts(output string) map[string]interface{} {
	trimmed := strings.TrimSpace(output)
	if idx := strings.Index(trimmed, "gathered_facts:"); idx >= 0 {
		var wrapper struct {
			GatheredFacts map[string]interface{} `yaml:"gathered_facts"`
		}
		if err := yaml.Unmarshal([]byte(trimmed[idx:]), &wrapper); err == nil && wrapper.GatheredFacts != nil {
			return normalizeFacts(wrapper.GatheredFacts)
		}
	}

	facts := make(map[string]interface{})
	// 简单解析 YAML 格式的 facts (回退处理)
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	inFacts := false
	var currentParentMap map[string]interface{}
	for _, line := range lines {
		if line == "gathered_facts:" {
			inFacts = true
			continue
		}
		if inFacts {
			if len(line) > 4 && line[0:4] == "    " && currentParentMap != nil {
				subLine := strings.TrimSpace(line[4:])
				if colon := strings.Index(subLine, ":"); colon != -1 {
					subKey := strings.TrimSpace(subLine[:colon])
					subVal := strings.Trim(strings.TrimSpace(subLine[colon+1:]), "\"'")
					currentParentMap[subKey] = subVal
				}
				continue
			}
			if len(line) > 2 && line[0:2] == "  " {
				currentParentMap = nil
				colon := strings.Index(line[2:], ":")
				if colon != -1 {
					key := strings.TrimSpace(line[2 : 2+colon])
					val := ""
					if 2+colon+1 < len(line) {
						val = strings.Trim(strings.TrimSpace(line[2+colon+1:]), "\"'")
					}
					if val == "" {
						subMap := make(map[string]interface{})
						facts[key] = subMap
						currentParentMap = subMap
					} else {
						facts[key] = val
					}
				}
			}
		}
	}
	return normalizeFacts(facts)
}

func normalizeFacts(facts map[string]interface{}) map[string]interface{} {
	if facts == nil {
		return nil
	}
	if rawDist, ok := facts["ansible_distribution"]; ok {
		dist := strings.TrimSpace(fmt.Sprintf("%v", rawDist))
		if idx := strings.Index(dist, "||"); idx != -1 {
			dist = strings.TrimSpace(dist[:idx])
		}
		dist = strings.Trim(dist, "\"'")
		lower := strings.ToLower(dist)
		switch {
		case strings.Contains(lower, "ubuntu"):
			facts["ansible_distribution"] = "Ubuntu"
		case strings.Contains(lower, "debian"):
			facts["ansible_distribution"] = "Debian"
		case strings.Contains(lower, "centos"):
			facts["ansible_distribution"] = "CentOS"
		case strings.Contains(lower, "red hat") || strings.Contains(lower, "rhel"):
			facts["ansible_distribution"] = "RedHat"
		case strings.Contains(lower, "rocky"):
			facts["ansible_distribution"] = "Rocky"
		case strings.Contains(lower, "almalinux") || strings.Contains(lower, "alma"):
			facts["ansible_distribution"] = "AlmaLinux"
		case strings.Contains(lower, "alpine"):
			facts["ansible_distribution"] = "Alpine"
		case strings.Contains(lower, "fedora"):
			facts["ansible_distribution"] = "Fedora"
		case strings.Contains(lower, "amazon"):
			facts["ansible_distribution"] = "Amazon"
		case strings.Contains(lower, "arch"):
			facts["ansible_distribution"] = "Archlinux"
		case strings.Contains(lower, "darwin") || strings.Contains(lower, "mac"):
			facts["ansible_distribution"] = "MacOSX"
		case dist != "":
			facts["ansible_distribution"] = dist
		}
	}
	if rawVer, ok := facts["ansible_distribution_version"]; ok {
		ver := strings.TrimSpace(fmt.Sprintf("%v", rawVer))
		if idx := strings.Index(ver, "||"); idx != -1 {
			ver = strings.TrimSpace(ver[:idx])
		}
		facts["ansible_distribution_version"] = strings.Trim(ver, "\"'")
	}

	// 构造结构化 ansible_facts 命名空间字典
	ansibleFacts := make(map[string]interface{}, len(facts))
	for k, v := range facts {
		if k == "ansible_facts" {
			continue
		}
		ansibleFacts[k] = v
		if strings.HasPrefix(k, "ansible_") {
			shortKey := strings.TrimPrefix(k, "ansible_")
			if _, exists := ansibleFacts[shortKey]; !exists {
				ansibleFacts[shortKey] = v
			}
		}
	}
	facts["ansible_facts"] = ansibleFacts

	return facts
}

func (e *Executor) parseFacts(output string) map[string]interface{} {
	return parseFacts(output)
}
