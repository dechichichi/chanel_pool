package workpool

import (
	"runtime/debug"
	"time"
)

// worker 接口定义
type worker interface {
	run()
	finish()
	lastUsedTime() time.Time
	setLastUsedTime(t time.Time)
	inputFunc(func())
}

// goWorker 实际执行任务的 worker
type goWorker struct {
	pool     *Pool
	task     chan func()
	lastUsed time.Time
}

// run 启动 worker goroutine
func (w *goWorker) run() {
	w.pool.addRunning(1)
	go func() {
		defer func() {
			if w.pool.addRunning(-1) == 0 && w.pool.IsClosed() {
				w.pool.once.Do(func() {
					close(w.pool.allDone)
				})
			}
			w.pool.workerCache.Put(w)
			if p := recover(); p != nil {
				if w.pool.panicHandler != nil {
					w.pool.panicHandler(p)
				} else {
					w.pool.logger.Printf("worker panic: %v\n%s\n", p, debug.Stack())
				}
			}
			w.pool.cond.Signal()
		}()

		for fn := range w.task {
			if fn == nil {
				return
			}
			fn()
			if ok := w.pool.revertWorker(w); !ok {
				return
			}
		}
	}()
}

func (w *goWorker) finish() {
	w.task <- nil
}

func (w *goWorker) lastUsedTime() time.Time {
	return w.lastUsed
}

func (w *goWorker) setLastUsedTime(t time.Time) {
	w.lastUsed = t
}

func (w *goWorker) inputFunc(fn func()) {
	w.task <- fn
}
