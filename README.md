# AW-LWW-Map (Go)

添加胜出的最后写入键值映射表（Add-Wins Last-Writer-Wins Map），一个仅依赖
Go 标准库的分布式并发 Map CRDT。

## 语义

- 根键（root key）下挂动态增删改的子字段（sub-field）。
- 每次写入被打上全局唯一的 dot `(node, seq)`。
- 删除（`RemoveKey` / `RemoveField`）记录其**因果观察到**的 dot 集合作为墓碑。
- 合并时，未被墓碑覆盖的并发写入存活 —— **添加胜出（add-wins）**：
  并发的“删除键”与“更新键内字段”冲突时，更新优先。
- 同一字段的并发写入按 `(timestamp, node)` 做 **最后写入胜出（LWW）**。
- `Merge` 满足结合律、交换律、幂等律，副本间互相合并即可收敛。
- 所有操作加锁，goroutine 安全。

## API

```go
m := awlwwmap.New("nodeA")
m.Set("user_profile", "age", 25)          // 本地时钟写入
m.SetAt("user_profile", "age", 26, ts)    // 显式时间戳写入
m.RemoveKey("user_profile")               // 删除根键
m.RemoveField("user_profile", "age")      // 删除子字段
fields, ok := m.Get("user_profile")       // 读取整个键
v, ok := m.GetField("user_profile", "age") // 读取子字段
keys := m.Keys()                          // 存活的根键
m.Merge(other)                            // 因果合并
```

## 验收场景

节点 A 删除根键 `user_profile`，节点 B 并发修改 `user_profile.age=25`。
因果合并判定更新优先，保留 `user_profile` 及 `age` 字段（A 观察到的
旧字段 `name`/`city` 仍被删除）。见
`TestAcceptance_RemoveKeyVsConcurrentFieldUpdate`。

## 运行

```sh
go test ./...        # 运行全部测试
go test -race ./...  # 竞态检测
go run ./cmd/example # 运行样例
```
