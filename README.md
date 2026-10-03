# wireopt — Protobuf Wire 解析器 + 稀疏数据内存紧凑优化器（Go，零外部依赖）

仅使用 Go 标准库实现。

## 组件

- `wire/`：纯手工 Protocol Buffers 二进制 wire-format 解析器
  - Varint 7 位压缩编解码（含 64 位溢出检测）
  - ZigZag 有符号整数转换
  - Fixed32 / Fixed64 小端定长编解码
  - Length-delimited 嵌套消息（反射模式校验引擎 Decoder，带深度/长度安全限制）
  - Packed Repeated 数组解码（varint / zigzag / fixed32 / fixed64）
  - 截断安全：所有越界路径返回 TruncatedError（含字段号与缺口字节数），绝不 panic
- `sparse/`：海量稀疏数据内存紧凑优化器
  - 容器自适应转换：稀疏态（map）与稠密态（slice + 占用位图）按密度阈值带迟滞自动切换
  - 压缩快照：索引 delta-varint + 值 zigzag-varint，针对稀疏偏斜分布极致压缩
  - 全部操作由 RWMutex 保护，并发安全
- `cmd/demo/`：运行样例

## 运行

    go test ./...          # 单元测试
    go test -race ./...    # 并发竞态检测
    go run ./cmd/demo      # 运行样例
