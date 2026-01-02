package workpool

import (
	"context"
	"errors"
	"log"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultPoolSize       = math.MaxInt32
	DefaultCleanInterval  = time.Second
	nowTimeUpdateInterval = 500 * time.Millisecond
)

const (
	OPENED = iota
	CLOSED
)

var (
	ErrPoolClosed   = errors.New("pool has been closed")
	ErrPoolOverload = errors.New("pool overload")
	ErrTimeout      = errors.New("operation timed out")

	workerChanCap = func() int {
		if runtime.GOMAXPROCS(0) == 1 {
			return 0
		}
		return 1
	}()
)

// Logger 日志接口
type Logger interface {
	Printf(format string, args ...any)
}

// Pool goroutine 池
type Pool struct {
	capacity     int32
	running      int32
	waiting      int32
	state        int32
	lock         sync.Locker
	workers      *workerStack
	cond         *sync.Cond
	workerCache  sync.Pool
	allDone      chan struct{}
	once         *sync.Once
	now          atomic.Value
	purgeCtx     context.Context
	stopPurge    context.CancelFunc
	ticktockCtx  context.Context
	stopTicktock context.CancelFunc

	// 配置选项
	expiryDuration   time.Duration
	maxBlockingTasks int
	nonblocking      bool
	panicHandler     func(any)
	logger           Logger
}

// Option 配置函数
type Option func(*Pool)

// WithExpiryDuration 设置 worker 过期时间
func WithExpiryDuration(d time.Duration) Option {
	return func(p *Pool) { p.expiryDuration = d }
}

// WithMaxBlockingTasks 设置最大阻塞任务数
func WithMaxBlockingTasks(n int) Option {
	return func(p *Pool) { p.maxBlockingTasks = n }
}

// WithNonblocking 设置非阻塞模式
func WithNonblocking(b bool) Option {
	return func(p *Pool) { p.nonblocking = b }
}

// WithPanicHandler 设置 panic 处理函数
func WithPanicHandler(h func(any)) Option {
	return func(p *Pool) { p.panicHandler = h }
}

// WithLogger 设置日志
func WithLogger(l Logger) Option {
	return func(p *Pool) { p.logger = l }
}

// NewPool 创建 goroutine 池
func NewPool(size int, opts ...Option) (*Pool, error) {
	if size <= 0 {
		size = -1
	}

	p := &Pool{
		capacity:       int32(size),
		lock:           NewSpinLock(),
		workers:        newWorkerStack(0),
		allDone:        make(chan struct{}),
		once:           &sync.Once{},
		expiryDuration: DefaultCleanInterval,
		logger:         log.New(os.Stderr, "[workpool]: ", log.LstdFlags),
	}

	for _, opt := range opts {
		opt(p)
	}

	p.workerCache.New = func() any {
		return &goWorker{
			pool: p,
			task: make(chan func(), workerChanCap),
		}
	}

	p.cond = sync.NewCond(p.lock)
	p.goPurge()
	p.goTicktock()

	return p, nil
}

// Submit 提交任务
func (p *Pool) Submit(task func()) error {
	if p.IsClosed() {
		return ErrPoolClosed
	}
	w, err := p.retrieveWorker()
	if w != nil {
		w.inputFunc(task)
	}
	return err
}

// Running 返回正在运行的 worker 数量
func (p *Pool) Running() int {
	return int(atomic.LoadInt32(&p.running))
}

// Free 返回可用 worker 数量
func (p *Pool) Free() int {
	c := p.Cap()
	if c < 0 {
		return -1
	}
	return c - p.Running()
}

// Waiting 返回等待中的任务数量
func (p *Pool) Waiting() int {
	return int(atomic.LoadInt32(&p.waiting))
}

// Cap 返回池容量
func (p *Pool) Cap() int {
	return int(atomic.LoadInt32(&p.capacity))
}

// Tune 动态调整池容量
func (p *Pool) Tune(size int) {
	capacity := p.Cap()
	if capacity == -1 || size <= 0 || size == capacity {
		return
	}
	atomic.StoreInt32(&p.capacity, int32(size))
	if size > capacity {
		if size-capacity == 1 {
			p.cond.Signal()
			return
		}
		p.cond.Broadcast()
	}
}

// IsClosed 检查池是否已关闭
func (p *Pool) IsClosed() bool {
	return atomic.LoadInt32(&p.state) == CLOSED
}

// Release 关闭池
func (p *Pool) Release() {
	if !atomic.CompareAndSwapInt32(&p.state, OPENED, CLOSED) {
		return
	}
	if p.stopPurge != nil {
		p.stopPurge()
	}
	if p.stopTicktock != nil {
		p.stopTicktock()
	}
	p.lock.Lock()
	p.workers.reset()
	p.lock.Unlock()
	p.cond.Broadcast()
}

// ReleaseTimeout 带超时的优雅关闭
func (p *Pool) ReleaseTimeout(timeout time.Duration) error {
	if p.IsClosed() {
		return ErrPoolClosed
	}
	p.Release()

	if p.Running() == 0 {
		p.once.Do(func() { close(p.allDone) })
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ErrTimeout
	case <-p.allDone:
		return nil
	}
}

// Reboot 重启池
func (p *Pool) Reboot() {
	if atomic.CompareAndSwapInt32(&p.state, CLOSED, OPENED) {
		p.goPurge()
		p.goTicktock()
		p.allDone = make(chan struct{})
		p.once = &sync.Once{}
	}
}

func (p *Pool) addRunning(delta int) int {
	return int(atomic.AddInt32(&p.running, int32(delta)))
}

func (p *Pool) addWaiting(delta int) {
	atomic.AddInt32(&p.waiting, int32(delta))
}

func (p *Pool) nowTime() time.Time {
	return p.now.Load().(time.Time)
}

// retrieveWorker 获取可用 worker
func (p *Pool) retrieveWorker() (w worker, err error) {
	p.lock.Lock()

retry:
	if w = p.workers.detach(); w != nil {
		p.lock.Unlock()
		return
	}

	if capacity := p.Cap(); capacity == -1 || capacity > p.Running() {
		w = p.workerCache.Get().(worker)
		w.run()
		p.lock.Unlock()
		return
	}

	if p.nonblocking || (p.maxBlockingTasks != 0 && p.Waiting() >= p.maxBlockingTasks) {
		p.lock.Unlock()
		return nil, ErrPoolOverload
	}

	p.addWaiting(1)
	p.cond.Wait()
	p.addWaiting(-1)

	if p.IsClosed() {
		p.lock.Unlock()
		return nil, ErrPoolClosed
	}

	goto retry
}

// revertWorker 归还 worker
func (p *Pool) revertWorker(w worker) bool {
	if capacity := p.Cap(); (capacity > 0 && p.Running() > capacity) || p.IsClosed() {
		p.cond.Broadcast()
		return false
	}

	w.setLastUsedTime(p.nowTime())

	p.lock.Lock()
	if p.IsClosed() {
		p.lock.Unlock()
		return false
	}
	_ = p.workers.insert(w)
	p.cond.Signal()
	p.lock.Unlock()

	return true
}

// purgeStaleWorkers 定期清理过期 worker
func (p *Pool) purgeStaleWorkers() {
	ticker := time.NewTicker(p.expiryDuration)
	defer ticker.Stop()

	for {
		select {
		case <-p.purgeCtx.Done():
			return
		case <-ticker.C:
		}

		if p.IsClosed() {
			break
		}

		var isDormant bool
		p.lock.Lock()
		staleWorkers := p.workers.refresh(p.expiryDuration)
		n := p.Running()
		isDormant = n == 0 || n == len(staleWorkers)
		p.lock.Unlock()

		for i := range staleWorkers {
			staleWorkers[i].finish()
			staleWorkers[i] = nil
		}

		if isDormant && p.Waiting() > 0 {
			p.cond.Broadcast()
		}
	}
}

// ticktock 定期更新时间戳
func (p *Pool) ticktock() {
	ticker := time.NewTicker(nowTimeUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ticktockCtx.Done():
			return
		case <-ticker.C:
		}

		if p.IsClosed() {
			break
		}
		p.now.Store(time.Now())
	}
}

func (p *Pool) goPurge() {
	p.purgeCtx, p.stopPurge = context.WithCancel(context.Background())
	go p.purgeStaleWorkers()
}

func (p *Pool) goTicktock() {
	p.now.Store(time.Now())
	p.ticktockCtx, p.stopTicktock = context.WithCancel(context.Background())
	go p.ticktock()
}
