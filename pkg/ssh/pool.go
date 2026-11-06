package ssh

import (
	"fmt"
	"sync"

	"go-ansible/pkg/inventory"
)

// ConnectionPool 连接池，统一管理 SSH、Local、Docker 等各类型连接
type ConnectionPool struct {
	config    *ClientConfig
	clients   map[string]Connection
	mu        sync.RWMutex
	hostLocks sync.Map // map[string]*sync.Mutex，细粒度主机握手锁
}

// NewConnectionPool 创建新的连接池
func NewConnectionPool(config *ClientConfig) *ConnectionPool {
	if config == nil {
		config = DefaultConfig()
	}
	return &ConnectionPool{
		config:  config,
		clients: make(map[string]Connection),
	}
}

func (p *ConnectionPool) getHostLock(hostName string) *sync.Mutex {
	v, _ := p.hostLocks.LoadOrStore(hostName, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Get 获取或创建指定主机的连接 (SSH/Local/Docker/WinRM)
// 采用细粒度分片并发锁：读已建连接走 RLock，未建连接通过单主机互斥锁并发握手，
// 彻底避免高并发（forks=50/100）下网络握手阻塞全局连接池。
func (p *ConnectionPool) Get(host *inventory.Host) (Connection, error) {
	p.mu.RLock()
	if conn, ok := p.clients[host.Name]; ok {
		p.mu.RUnlock()
		if hSetter, ok := conn.(interface{ SetHost(*inventory.Host) }); ok {
			hSetter.SetHost(host)
		}
		return conn, nil
	}
	p.mu.RUnlock()

	// 获取该主机的独立互斥锁，多主机并发握手互不阻塞
	hMu := p.getHostLock(host.Name)
	hMu.Lock()
	defer hMu.Unlock()

	// Double-check inside per-host lock
	p.mu.RLock()
	if conn, ok := p.clients[host.Name]; ok {
		p.mu.RUnlock()
		if hSetter, ok := conn.(interface{ SetHost(*inventory.Host) }); ok {
			hSetter.SetHost(host)
		}
		return conn, nil
	}
	p.mu.RUnlock()

	conn, err := NewConnection(host, p.config)
	if err != nil {
		return nil, fmt.Errorf("create connection for %s: %w", host.Name, err)
	}

	if err := conn.Connect(); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", host.Name, err)
	}

	p.mu.Lock()
	p.clients[host.Name] = conn
	p.mu.Unlock()

	return conn, nil
}

// Remove 移除连接
func (p *ConnectionPool) Remove(hostName string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 保留 p.hostLocks 中的主机互斥锁指针稳定，避免并发握手互斥失效与泄露
	if conn, ok := p.clients[hostName]; ok {
		err := conn.Close()
		delete(p.clients, hostName)
		return err
	}
	return nil
}

// Store 将已创建的连接放入缓存。
func (p *ConnectionPool) Store(hostName string, conn Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[hostName] = conn
}

// Has 判断指定主机是否已有缓存连接。
func (p *ConnectionPool) Has(hostName string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.clients[hostName]
	return ok
}

// Close 关闭所有连接
func (p *ConnectionPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var lastErr error
	for name, conn := range p.clients {
		if err := conn.Close(); err != nil {
			lastErr = err
		}
		delete(p.clients, name)
	}
	p.hostLocks.Range(func(key, _ interface{}) bool {
		p.hostLocks.Delete(key)
		return true
	})
	return lastErr
}

// ExecuteAll 并发执行命令，限制并发上限防止海量主机耗尽连接与句柄
func (p *ConnectionPool) ExecuteAll(hosts []*inventory.Host, cmd string) (map[string]*Result, error) {
	if len(hosts) == 0 {
		return make(map[string]*Result), nil
	}
	results := make(map[string]*Result, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(hosts))

	maxConcurrent := 50
	if len(hosts) < maxConcurrent {
		maxConcurrent = len(hosts)
	}
	sem := make(chan struct{}, maxConcurrent)

	for _, host := range hosts {
		wg.Add(1)
		sem <- struct{}{}
		go func(h *inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			conn, err := p.Get(h)
			if err != nil {
				errChan <- err
				return
			}

			result, err := conn.Execute(cmd)
			if err != nil {
				errChan <- err
				return
			}

			mu.Lock()
			results[h.Name] = result
			mu.Unlock()
		}(host)
	}

	wg.Wait()
	close(errChan)

	// 检查错误
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return results, fmt.Errorf("execution errors: %v", errors)
	}

	return results, nil
}
