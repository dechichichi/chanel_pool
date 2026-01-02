package workpool

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// spinLock 自旋锁实现，使用指数退避算法减少锁竞争
type spinLock uint32

const maxBackoff = 16

func (sl *spinLock) Lock() {
	backoff := 1
	for !atomic.CompareAndSwapUint32((*uint32)(sl), 0, 1) {
		for i := 0; i < backoff; i++ {
			runtime.Gosched()
		}
		if backoff < maxBackoff {
			backoff <<= 1
		}
	}
}

func (sl *spinLock) Unlock() {
	atomic.StoreUint32((*uint32)(sl), 0)
}

// NewSpinLock 创建自旋锁
func NewSpinLock() sync.Locker {
	return new(spinLock)
}
