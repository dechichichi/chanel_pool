# go-workpool

基于 Go 语言实现的高性能 goroutine 池，专注于高效管理并发任务与 goroutine 资源。

## 核心特性

- **Worker 生命周期管理**：通过 workerCache 缓存池减少 goroutine 频繁创建销毁的性能开销，结合 panic 捕获与自定义处理逻辑，提升组件稳定性
- **MultiPool 多池架构**：支持轮询（RoundRobin）与最少任务数（LeastTasks）两种负载均衡策略，通过动态容量调整（Tune）功能适配不同并发场景
- **优雅关闭与重启**：实现 ReleaseTimeout 优雅关闭与 Reboot 重启机制，结合自旋锁（SpinLock）同步控制，保障高并发场景下的资源竞争安全
- **自动清理**：定期清理过期空闲 worker，释放系统资源

## 安装

```bash
go get github.com/your-username/go-workpool
```

## 快速开始

### 基本使用

```go
package main

import (
    "fmt"
    "time"
    "go-workpool"
)

func main() {
    // 创建容量为 10 的协程池
    pool, _ := workpool.NewPool(10)
    defer pool.Release()

    // 提交任务
    for i := 0; i < 100; i++ {
        n := i
        pool.Submit(func() {
            fmt.Printf("Task %d running\n", n)
            time.Sleep(100 * time.Millisecond)
        })
    }

    time.Sleep(2 * time.Second)
}
```

### 配置选项

```go
pool, _ := workpool.NewPool(10,
    workpool.WithExpiryDuration(5*time.Second),    // worker 过期时间
    workpool.WithMaxBlockingTasks(100),            // 最大阻塞任务数
    workpool.WithNonblocking(true),                // 非阻塞模式
    workpool.WithPanicHandler(func(p any) {        // panic 处理
        fmt.Printf("panic: %v\n", p)
    }),
)
```

### 多池负载均衡

```go
// 创建 4 个池，每个池容量 10，使用轮询策略
mp, _ := workpool.NewMultiPool(4, 10, workpool.RoundRobin)
defer mp.ReleaseTimeout(5 * time.Second)

// 提交任务会自动分配到不同的池
mp.Submit(func() {
    // your task
})

// 使用最少任务策略
mp2, _ := workpool.NewMultiPool(4, 10, workpool.LeastTasks)
```

## API

### Pool

| 方法 | 说明 |
|------|------|
| `NewPool(size int, opts ...Option)` | 创建协程池 |
| `Submit(task func()) error` | 提交任务 |
| `Running() int` | 获取运行中的 worker 数量 |
| `Free() int` | 获取空闲 worker 数量 |
| `Waiting() int` | 获取等待中的任务数量 |
| `Cap() int` | 获取池容量 |
| `Tune(size int)` | 动态调整容量 |
| `IsClosed() bool` | 检查池是否关闭 |
| `Release()` | 关闭池 |
| `ReleaseTimeout(timeout time.Duration) error` | 带超时的优雅关闭 |
| `Reboot()` | 重启池 |

### MultiPool

| 方法 | 说明 |
|------|------|
| `NewMultiPool(size, sizePerPool int, lbs LoadBalancingStrategy, opts ...Option)` | 创建多池 |
| `Submit(task func()) error` | 提交任务（自动负载均衡） |
| `Running() int` | 获取所有池运行中的 worker 总数 |
| `Free() int` | 获取所有池空闲 worker 总数 |
| `Cap() int` | 获取总容量 |
| `Tune(size int)` | 调整每个池的容量 |
| `ReleaseTimeout(timeout time.Duration) error` | 带超时的优雅关闭 |
| `Reboot()` | 重启所有池 |

## 负载均衡策略

- `RoundRobin`：轮询分配任务到各个池
- `LeastTasks`：将任务分配到当前运行任务最少的池

## License

MIT
