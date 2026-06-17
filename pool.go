package wasmloader

import (
	"context"
	"fmt"
	"sync"
)

// InstancePool pre-instantiates N independent modules — each with its own linear memory —
// and hands them out one-at-a-time so no two concurrent callers share an instance (SPEC §6.2).
// Distributing state across independent memories extends longevity under bump allocators.
// Appropriate for servers and loop-intensive workloads.
type InstancePool struct {
	path     string
	cb       *Callbacks
	size     int
	ch       chan *Module // buffered: acts as the available-instance semaphore
	initOnce sync.Once
	initErr  error
}

// NewInstancePool creates a pool of `size` instances (default 4 if size <= 0). Instances are
// lazily created on the first Acquire/Run.
func NewInstancePool(path string, size int, cbs ...*Callbacks) *InstancePool {
	if size <= 0 {
		size = 4
	}
	return &InstancePool{
		path: path,
		cb:   firstCallbacks(cbs),
		size: size,
		ch:   make(chan *Module, size),
	}
}

func (p *InstancePool) load() (*Module, error) {
	if p.cb != nil {
		return Load(p.path, p.cb)
	}
	return Load(p.path)
}

func (p *InstancePool) ensureInit() error {
	p.initOnce.Do(func() {
		for i := 0; i < p.size; i++ {
			m, err := p.load()
			if err != nil {
				p.initErr = fmt.Errorf("wasmloader: pool init (instance %d/%d): %w", i+1, p.size, err)
				return
			}
			p.ch <- m
		}
	})
	return p.initErr
}

// Acquire checks out an available instance, blocking until one is free.
func (p *InstancePool) Acquire() (*Module, error) {
	if err := p.ensureInit(); err != nil {
		return nil, err
	}
	return <-p.ch, nil
}

// AcquireContext is like Acquire but returns ctx.Err() if the context is cancelled while
// waiting for a free instance.
func (p *InstancePool) AcquireContext(ctx context.Context) (*Module, error) {
	if err := p.ensureInit(); err != nil {
		return nil, err
	}
	select {
	case m := <-p.ch:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Release returns an instance to the pool.
func (p *InstancePool) Release(m *Module) {
	p.ch <- m
}

// Run atomically acquires an instance, calls fn with it, and releases it — even if fn errors.
func (p *InstancePool) Run(fn func(*Module) (any, error)) (any, error) {
	m, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(m)
	return fn(m)
}

// Close releases every instance's runtime. The pool must not be used after Close.
func (p *InstancePool) Close() error {
	if err := p.ensureInit(); err != nil {
		return err
	}
	var firstErr error
	for i := 0; i < p.size; i++ {
		m := <-p.ch
		if err := m.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
