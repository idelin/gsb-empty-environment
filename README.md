# slotmig — 可重放的槽位迁移模拟器

仅用 Go 标准库。数据与消息全部本地合成；无 goroutine、无时钟、无真实网络/数据库，
所有交错由显式指令脚本驱动，因此每一次运行都可逐字节重放。

## 模型

- **放置**：`Digest`（FNV-1a）为每个节点导出 `offset = d % M` 与 `skip = 1 + d % (M-1)`；
  M 为素数保证排列覆盖全部槽。节点按 ID 排序逐轮占据排列中首个空槽。
  备节点从不同故障域选取；缺少合法备节点时显式报错（`sim/placement.go`）。
- **配置**：每次换版输出确实换主的槽位集合（`DiffConfig`）；每槽独立递增 epoch。
  持旧配置的请求仍按旧路由发到旧主，但旧主用持久化 fence 拒绝——不能无条件写旧主。
- **槽日志**：整数序号的键值变更日志，删除也是变更；写请求带幂等 ID，
  每次成功写入在其所属 epoch 内唯一，幂等结果随日志一起复制。
- **迁移**：基线（≤baselineSeq）→ 增量（允许乱序/重复，目标只确认连续前缀，
  同序号不同内容报错）→ 冻结（确定 finalSeq 并持久化 fence，旧主拒绝新写，
  期间新写返回 Retry 绝不确认）→ 追赶（目标持久化完整前缀）→ 决议持久化后
  新主才以新 epoch 接受写。决议前无双主，决议后旧主 fence 永不解除。
- **恢复**：日志、幂等结果、fence、决议、已确认进度都在持久化状态里；
  崩溃只丢在途消息与目标乱序暂存，恢复后从已确认进度续传，不丢记录。
- **背压**：在途消息 + 目标暂存全局合计不得超过 B 字节，超出即暂停发送（记录保留）。

## 用法

```sh
go run ./cmd/sim -list                      # 列出场景
go run ./cmd/sim -scenario full -M 7 -B 1048576
go run ./cmd/sim -scenario capacity -B 100  # 容量耗尽/背压
go run ./cmd/sim -scenario full -record script.json
go run ./cmd/sim -replay script.json -expect <摘要>   # 故障重放并校验
go test ./...                               # 不变量与场景测试
```

场景：`full`（基线期间改同键+乱序补缺+删除重建+跨切换重复+目标崩溃+fence）、
`capacity`（背压不丢记录）、`restart-boundary`（每个持久化边界重启）、
`conflict`（同序号不同内容，检查器必须检出）、`multi-slot`（多槽独立推进）。

## 独立不变量检查器

`sim/checker.go` 只消费证据事件流与最终 Dump，与模拟器内部状态解耦。检查：
fence 后旧主零确认、决议后旧主零回退、同 epoch 无双主写、epoch 严格递增、
幂等唯一（同 idem 不重复 OK、同 (epoch,seq) 不被复用）、Dup 必有对应原始 OK、
冲突事件即违规；并用单副本串行键值模型按 (slot, epoch, seq) 重放全部已确认写，
与最终主节点状态逐键比对，且每条已确认写在权威日志中恰好出现一次。
