package ssh

import (
	"fmt"
	"sync"

	"go-ansible/pkg/inventory"
)

// ConnectionPool 连接池，统一管理 SSH、Local、Docker 等各类型连接
type ConnectionPool struct {
	config  *ClientConfig
	clients map[string]Connection
	mu      sync.RWMutex
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

// Get 获取或创建指定主机的连接 (SSH/Local/Docker/WinRM)
func (p *ConnectionPool) Get(host *inventory.Host) (Connection, error) {
	p.mu.RLock()
	if conn, ok := p.clients[host.Name]; ok {
		p.mu.RUnlock()
		return conn, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// double check
	if conn, ok := p.clients[host.Name]; ok {
		return conn, nil
	}

	conn, err := NewConnection(host, p.config)
	if err != nil {
		return nil, fmt.Errorf("create connection for %s: %w", host.Name, err)
	}

	if err := conn.Connect(); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", host.Name, err)
	}

	p.clients[host.Name] = conn
	return conn, nil
}

// Remove 移除连接
func (p *ConnectionPool) Remove(hostName string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

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
	return lastErr
}

// ExecuteAll 并发执行命令
func (p *ConnectionPool) ExecuteAll(hosts []*inventory.Host, cmd string) (map[string]*Result, error) {
	results := make(map[string]*Result)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(hosts))

	for _, host := range hosts {
		wg.Add(1)
		go func(h *inventory.Host) {
			defer wg.Done()

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
