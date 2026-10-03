# compact — 零依赖 Protobuf Wire 编解码 + 增量快照/检查点归档 (Go)

纯手工、零外部依赖（仅标准库）的三个组件：

## 组件

- **`wire/`** — 二进制紧凑变长编解码器
  - Varint 7 位压缩编码（含 10 字节上限与溢出检测）
  - ZigZag 有符号整数转换
  - Fixed32 / Fixed64 小端定长编码
  - Length-delimited 嵌套消息，所有读取均做边界检查，截断输入返回
    `ErrTruncated`，绝不 panic / 越界
- **`proto/`** — 反射模式编解码与校验引擎
  - 结构体标签 `` `wire:"1,varint"` `` 映射字段号与编码类型
  - 支持多层嵌套消息、Packed Repeated 数组、未知字段安全跳过
  - `check:"required,min=1,max=100,minlen=1"` 反射校验，聚合全部违规路径
- **`snapshot/`** — 海量状态下的轻量级增量快照与检查点归档
  - 分层写时复制（base + pending + overlay），检查点合并与落盘在锁外完成，
    写入停顿为 O(1) 而非 O(状态规模)
  - 版本化 journal 支持增量 delta 归档，检查点后自动压缩
  - 归档文件用本仓库自有 wire 格式编码，临时文件 + rename 原子写入
  - 恢复 = 最新 checkpoint + 依次重放 delta（含墓碑删除）

## 运行

```sh
go test ./...          # 全部单元测试
go test -race ./...    # 并发安全（数据竞争检测）
go run .               # 端到端演示：编码/解码/校验/截断防护/快照归档恢复
```

## 测试覆盖

- varint 边界（0/127/128/2^63/MaxUint64）、溢出与 11 字节非法 varint
- zigzag 全边界（MinInt64/MaxInt64）
- 每个字节位置截断的安全性（不 panic、不越界）+ 字段中间截断必报错
- 三层嵌套消息 + packed repeated 往返一致性、未知字段跳过
- 校验引擎违规路径聚合（required/min/max/minlen/maxlen、嵌套路径）
- checkpoint 恢复往返、增量 delta 链恢复、损坏/截断归档拒绝
- journal 压缩、重复检查点拒绝、中止检查点状态回滚（墓碑保留）
- 8 并发写者 + 并发检查点/归档（`-race` 验证），最终状态精确一致
- 无 goroutine 泄漏、无内部状态残留断言
