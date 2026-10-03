# protodrr

零外部依赖（仅标准库）的两个组件：

- **`wire`** — 纯手工 Protocol Buffers 二进制 wire-format 编解码器 + 反射校验引擎
  - Varint 7 位压缩编码（最长 10 字节，溢出检测）
  - ZigZag 有符号整数变换（sint32/sint64）
  - Fixed32/Fixed64 小端定长编解码
  - Length-delimited 嵌套消息、Packed Repeated 数组
  - 结构体标签反射编解码：`protobuf:"<kind>,<num>[,packed]"`
  - 反射校验引擎：`validate:"nonzero,min=1,max=100"`，递归嵌套结构
  - 全程边界检查：截断、长度越界、非法 wire type 均返回带字段号的 `*FieldError`，绝不越界读取

- **`scheduler`** — 多租户隔离 + Deficit Round Robin 加权公平调度器
  - 每租户独立 FIFO 队列，队列长度与在途任务双配额硬隔离
  - DRR：每轮按 `weight × quantum` 充值 deficit，头部任务 cost 不超 deficit 即派发
  - 大任务 deficit 跨轮累积；配额耗尽即拒绝（`ErrQueueFull`），不影响其他租户
  - 互斥锁保护全部状态，并发安全；`Close` 强制断言零泄漏（队列/在途必须清空）

## 运行

```sh
go test ./...          # 全部单元测试
go test -race ./...    # 含竞态检测
go run ./cmd/demo      # 运行样例
```
