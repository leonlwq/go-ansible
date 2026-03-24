package ssh

import (
	"fmt"
	"sync"

	"go-ansible/pkg/inventory"
)

// ConnectionPool 连接池
type ConnectionPool struct {
	config  *ClientConfig
	clients map[string]*Client
	mu      sync.RWMutex
}

// NewConnectionPool 创建新的连接池
func NewConnectionPool(config *ClientConfig) *ConnectionPool {
	if config == nil {
		config = DefaultConfig()
	}
	return &ConnectionPool{
		config:  config,
		clients: make(map[string]*Client),
	}
}

// Get 获取或创建客户端连接
func (p *ConnectionPool) Get(host *inventory.Host) (*Client, error) {
	p.mu.RLock()
	if client, ok := p.clients[host.Name]; ok {
		p.mu.RUnlock()
		return client, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// double check
	if client, ok := p.clients[host.Name]; ok {
		return client, nil
	}

	client, err := NewClient(host, p.config)
	if err != nil {
		return nil, fmt.Errorf("create client for %s: %w", host.Name, err)
	}

	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", host.Name, err)
	}

	p.clients[host.Name] = client
	return client, nil
}

// Remove 移除连接
func (p *ConnectionPool) Remove(hostName string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if client, ok := p.clients[hostName]; ok {
		err := client.Close()
		delete(p.clients, hostName)
		return err
	}
	return nil
}

// Close 关闭所有连接
func (p *ConnectionPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var lastErr error
	for name, client := range p.clients {
		if err := client.Close(); err != nil {
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

			client, err := p.Get(h)
			if err != nil {
				errChan <- err
				return
			}

			result, err := client.Execute(cmd)
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
