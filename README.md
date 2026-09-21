# fsmproto — 显式状态转移表协议机（Go 标准库）

## 结构

- `frame.go` — 增量帧解析器。线格式 `[2B 大端长度][1B 类型][负载]`。
  - 任意分块输入：`Parser.Feed` 累积字节，`Next` 对不完整帧返回
    `ErrNeedMoreData`（缺数据，可继续喂），对畸形帧返回 `*BadDataError`
    （坏数据，补字节也无法修复，应断连）。
  - 长度额度 `MaxFrameSize` 在任何分配之前检查，敌意长度字段无法触发
    大额分配。
- `machine.go` — 状态机。`Compile` 校验显式转移表（重复行、越界
  状态/事件即编译期错误）。`Apply` 原子执行：动作先跑，成功才提交
  输出并切换状态；动作失败则旧状态保留、未提交输出丢弃。
  - 未知消息类型 → `EvUnknown`；空闲超时 → `EvTimeout`；重复握手 /
    重入 → `EvReenter`，三者作为独立事件由表处理。
  - `Apply` 运行期间再次 `Apply` 返回 `ErrReentrant`。
  - 去重规则（`Dispatch`）：`seq == expected` 接受并推进；
    `seq < expected` 判重放——丢弃负载、重发确认、计数；
    `seq > expected` 判乱序——协议错误、状态不变。
- `table.go` — 默认转移表（握手门控：Init/Handshaking 无 Data 行，
  握手完成前业务帧一律 `ErrNoTransition`）。
- `cmd/demo` — 运行样例：`go run ./cmd/demo`

## 运行

```sh
go test ./...     # 全部测试
go run ./cmd/demo # 演示样例
```
