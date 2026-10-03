# protobuf-wire-faultlab

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎 + 异常注入/崩溃恢复容错验证 (TypeScript, Node.js >= 23.6 原生类型擦除运行, 无需编译)。

## 模块

| 文件 | 职责 |
| --- | --- |
| `src/varint.ts` | Varint 7 位压缩、ZigZag 有符号转换、int64/uint64 补码互转 (全程 bigint, 无精度丢失) |
| `src/wire.ts` | `WireReader` 安全游标 (每步边界检查) / `WireWriter` 编码器, 支持 Fixed32/64、嵌套 Length-delimited、Packed Repeated |
| `src/schema.ts` | 反射模式校验引擎: schema 驱动编解码, 校验 wire type / 枚举域 / required 约束, 未知字段安全跳过 |
| `src/crc32.ts` | 手工 CRC-32 (IEEE) |
| `src/faults.ts` | 确定性异常注入器 (xorshift32 种子): 断电 / 磁盘满 / 丢包, 概率 + 可编程触发点 |
| `src/device.ts` | 模拟块设备, 支持撕裂写 (torn write) 与重启 |
| `src/journal.ts` | 预写日志 (WAL): magic + CRC 记录, 恢复时截断撕裂尾部、回滚未提交事务、checkpoint 快照 |
| `src/store.ts` | 事务型 KV 存储: BEGIN→OP*→COMMIT 原子提交, 异步互斥锁保证并发串行, 故障时状态安全回滚 |
| `src/channel.ts` | 抗丢包可靠传输: 序号帧 + CRC, 缺口重传重组 |
| `src/tracker.ts` | 资源泄漏追踪器, 测试末尾断言严格无泄漏 |

## 运行

```bash
npm test    # node --test tests/   (40 个测试)
npm run demo # node examples/demo.ts
```

## 容错语义

- **突发断电**: 提交中途撕裂写 → 内存态立即回滚; 重启后扫描 WAL, CRC 校验失败处截断, 未提交事务丢弃, 已提交事务重放。
- **磁盘满**: 任意日志写入 ENOSPC → 事务整体回滚, 日志尾回卷, 故障解除后可继续工作。
- **网络丢包**: 帧级 CRC + 序号缺口重传, 40% 丢包率下仍逐字节还原消息。
- **字段截断**: 读端所有访问经边界检查, 抛 `TruncatedFieldError` (携带 needed/remaining/offset), 绝不越界读取。
