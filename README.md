# DMA 映射生命期与撤销证明

设备仍有在途 DMA 时，内存页不能回到普通分配池。本包为 Go 虚拟设备宿主提供页池、映射生命期、代次化操作令牌、DMA 队列模拟器和不透明停止证明。

## 安全不变量

- `NewMapping` 原子绑定页集合与映射代次；失败时不会留下部分绑定。
- 映射创建后即从普通分配池移除其页，只有满足释放条件时才归还。
- `Revoke` 之后 `Submit` 一律失败；撤销本身不会丢弃在途操作。
- 每个已接受操作必须被确认完成，最后一个引用释放时页才回到池中。
- `Complete` 幂等。重复或迟到的完成消息不会二次扣减在途计数，也不会提前重用页。
- 旧的映射代次由独立的 `Operation` 令牌隔离，不能用于新映射。
- `Reset` 除撤销映射外，还必须收到 `DMAQueue.StopProof`。证明只在队列完全停止、且该映射的全部已接受访问均完成或被栅栏隔离后签发；队列重启会使证明失效。
- 复位失败只留下“已撤销且仍固定”的状态，不会部分释放页。

## 用法

```go
pool := netsec.NewPagePool(2)
pages := make([]*netsec.Page, 0, 2)
for range 2 {
    page, err := pool.Allocate()
    if err != nil {
        return err
    }
    pages = append(pages, page)
}

m, err := netsec.NewMapping(pool, netsec.Generation(7), pages...)
if err != nil {
    return err
}

q := netsec.NewDMAQueue()
first, err := q.Submit(m)
if err != nil {
    return err
}
second, err := q.Submit(m)
if err != nil {
    return err
}

_ = q.Complete(first) // 部分完成：页仍被固定
m.Revoke()            // 不再接受新提交
// pool.FreeCount() == 0，因为 second 仍在途

_ = q.Complete(second)
// 这时两页才回到普通分配池
```

设备复位路径：

```go
m.Revoke()
if err := q.Halt(); err != nil { // 停止整条队列并栅栏在途操作
    return err
}
proof, err := q.StopProof(m) // 全队列停止证明，覆盖该映射全部已接受操作
if err != nil {
    return err
}
if err := m.Reset(proof); err != nil { // 无证明或证明过期时不释放页
    return err
}
```

## 验证

`mapping_test.go` 使用模拟 DMA 队列覆盖部分完成、重复完成、复位失败、跨映射/过期证明、直接提交未纳入证明以及并发完成等场景。

```sh
go test ./...
go test -race ./...
go vet ./...
```
