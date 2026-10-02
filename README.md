# hotring — 双环热备路由控制器

网络拓扑感知的主备哈希环（Go 标准库实现）：

- **主环（Active）** 承载全量流量，`Route` 通过 `atomic.Value` 无锁读取，零分配。
- **备环（Standby）** 后台同步拓扑变更（`PrepareStandby`），并通过 `Warmup`
  预计算键迁移影响（`MigrationReport`：迁移键数、各节点得失）。
- **原子翻转**：`Flip` 仅做一次原子指针存储，毫秒（实测微秒）级完成全局切换；
  环构建后不可变，飞行中的读请求继续使用旧环，无撕裂状态、零瞬时路由错乱。

## 运行

```sh
go test -race -v ./...   # 测试（含并发流量下原子翻转验收用例）
go run ./cmd/demo        # 运行样例：流量中注入缩容 → 预热 → 翻转
```

## 流程

```go
c := hotring.NewController(nodes, 160)  // 主环上线
c.PrepareStandby(smallerNodes)          // 后台注入缩容拓扑
report, _ := c.Warmup(knownKeys)        // 预计算迁移影响
c.Flip()                                // 原子指针翻转，流量瞬时平滑过渡
```
