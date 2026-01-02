package workpool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolBasic(t *testing.T) {
	pool, err := NewPool(10)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()

	var count int32
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		err := pool.Submit(func() {
			defer wg.Done()
			atomic.AddInt32(&count, 1)
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	wg.Wait()

	if count != 100 {
		t.Errorf("expected 100, got %d", count)
	}
}

func TestPoolPanicHandler(t *testing.T) {
	var panicCaught int32

	pool, _ := NewPool(10, WithPanicHandler(func(p any) {
		atomic.StoreInt32(&panicCaught, 1)
	}))
	defer pool.Release()

	pool.Submit(func() {
		panic("test panic")
	})

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&panicCaught) != 1 {
		t.Error("panic handler not called")
	}
}

func TestPoolTune(t *testing.T) {
	pool, _ := NewPool(10)
	defer pool.Release()

	if pool.Cap() != 10 {
		t.Errorf("expected cap 10, got %d", pool.Cap())
	}

	pool.Tune(20)

	if pool.Cap() != 20 {
		t.Errorf("expected cap 20, got %d", pool.Cap())
	}
}

func TestPoolReleaseTimeout(t *testing.T) {
	pool, _ := NewPool(10)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		pool.Submit(func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
		})
	}

	wg.Wait()
	err := pool.ReleaseTimeout(time.Second)
	if err != nil {
		t.Errorf("release timeout failed: %v", err)
	}
}

func TestPoolReboot(t *testing.T) {
	pool, _ := NewPool(10)
	pool.Release()

	if !pool.IsClosed() {
		t.Error("pool should be closed")
	}

	pool.Reboot()

	if pool.IsClosed() {
		t.Error("pool should be opened after reboot")
	}

	var count int32
	pool.Submit(func() {
		atomic.AddInt32(&count, 1)
	})

	time.Sleep(50 * time.Millisecond)
	pool.Release()

	if count != 1 {
		t.Error("task not executed after reboot")
	}
}

func TestMultiPool(t *testing.T) {
	mp, err := NewMultiPool(4, 10, RoundRobin)
	if err != nil {
		t.Fatal(err)
	}
	defer mp.ReleaseTimeout(time.Second)

	var count int32
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		mp.Submit(func() {
			defer wg.Done()
			atomic.AddInt32(&count, 1)
		})
	}

	wg.Wait()

	if count != 100 {
		t.Errorf("expected 100, got %d", count)
	}
}

func TestMultiPoolLeastTasks(t *testing.T) {
	mp, err := NewMultiPool(4, 10, LeastTasks)
	if err != nil {
		t.Fatal(err)
	}
	defer mp.ReleaseTimeout(time.Second)

	var count int32
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		mp.Submit(func() {
			defer wg.Done()
			atomic.AddInt32(&count, 1)
		})
	}

	wg.Wait()

	if count != 50 {
		t.Errorf("expected 50, got %d", count)
	}
}

func BenchmarkPool(b *testing.B) {
	pool, _ := NewPool(1000)
	defer pool.Release()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.Submit(func() {})
	}
}

func BenchmarkMultiPool(b *testing.B) {
	mp, _ := NewMultiPool(4, 250, RoundRobin)
	defer mp.ReleaseTimeout(time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mp.Submit(func() {})
	}
}
