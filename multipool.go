package workpool

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// LoadBalancingStrategy 负载均衡策略
type LoadBalancingStrategy int

const (
	RoundRobin LoadBalancingStrategy = iota + 1 // 轮询
	LeastTasks                                  // 最少任务
)

var (
	ErrInvalidMultiPoolSize         = errors.New("invalid multi-pool size")
	ErrInvalidLoadBalancingStrategy = errors.New("invalid load-balancing strategy")
)

// MultiPool 多池管理，支持负载均衡
type MultiPool struct {
	pools []*Pool
	index uint32
	state int32
	lbs   LoadBalancingStrategy
}

// NewMultiPool 创建多池
func NewMultiPool(size, sizePerPool int, lbs LoadBalancingStrategy, opts ...Option) (*MultiPool, error) {
	if size <= 0 {
		return nil, ErrInvalidMultiPoolSize
	}
	if lbs != RoundRobin && lbs != LeastTasks {
		return nil, ErrInvalidLoadBalancingStrategy
	}

	pools := make([]*Pool, size)
	for i := 0; i < size; i++ {
		pool, err := NewPool(sizePerPool, opts...)
		if err != nil {
			return nil, err
		}
		pools[i] = pool
	}

	return &MultiPool{
		pools: pools,
		index: math.MaxUint32,
		lbs:   lbs,
	}, nil
}

func (mp *MultiPool) next(lbs LoadBalancingStrategy) int {
	switch lbs {
	case RoundRobin:
		return int(atomic.AddUint32(&mp.index, 1) % uint32(len(mp.pools)))
	case LeastTasks:
		leastTasks := 1<<31 - 1
		idx := 0
		for i, pool := range mp.pools {
			if n := pool.Running(); n < leastTasks {
				leastTasks = n
				idx = i
			}
		}
		return idx
	}
	return -1
}

// Submit 提交任务
func (mp *MultiPool) Submit(task func()) error {
	if mp.IsClosed() {
		return ErrPoolClosed
	}
	err := mp.pools[mp.next(mp.lbs)].Submit(task)
	if err == ErrPoolOverload && mp.lbs == RoundRobin {
		return mp.pools[mp.next(LeastTasks)].Submit(task)
	}
	return err
}

// Running 返回所有池的运行中 worker 数量
func (mp *MultiPool) Running() int {
	n := 0
	for _, pool := range mp.pools {
		n += pool.Running()
	}
	return n
}

// Free 返回所有池的空闲 worker 数量
func (mp *MultiPool) Free() int {
	n := 0
	for _, pool := range mp.pools {
		n += pool.Free()
	}
	return n
}

// Waiting 返回所有池的等待任务数量
func (mp *MultiPool) Waiting() int {
	n := 0
	for _, pool := range mp.pools {
		n += pool.Waiting()
	}
	return n
}

// Cap 返回总容量
func (mp *MultiPool) Cap() int {
	n := 0
	for _, pool := range mp.pools {
		n += pool.Cap()
	}
	return n
}

// Tune 调整每个池的容量
func (mp *MultiPool) Tune(size int) {
	for _, pool := range mp.pools {
		pool.Tune(size)
	}
}

// IsClosed 检查是否关闭
func (mp *MultiPool) IsClosed() bool {
	return atomic.LoadInt32(&mp.state) == CLOSED
}

// ReleaseTimeout 带超时的优雅关闭
func (mp *MultiPool) ReleaseTimeout(timeout time.Duration) error {
	if !atomic.CompareAndSwapInt32(&mp.state, OPENED, CLOSED) {
		return ErrPoolClosed
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(mp.pools))

	for _, pool := range mp.pools {
		wg.Add(1)
		go func(p *Pool) {
			defer wg.Done()
			errCh <- p.ReleaseTimeout(timeout)
		}(pool)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil && err != ErrTimeout {
			return err
		}
	}
	return nil
}

// Reboot 重启多池
func (mp *MultiPool) Reboot() {
	if atomic.CompareAndSwapInt32(&mp.state, CLOSED, OPENED) {
		atomic.StoreUint32(&mp.index, 0)
		for _, pool := range mp.pools {
			pool.Reboot()
		}
	}
}
