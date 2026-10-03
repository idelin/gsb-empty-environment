# Protobuf Wire Codec + 稀疏数据内存紧凑优化器（TypeScript）

零外部依赖的 Protocol Buffers 二进制 Wire-format 解析器、反射模式校验引擎，
以及面向海量稀疏数据的自适应容器 / 压缩优化器。纯 TypeScript，使用 Node.js
内置测试运行器（`node:test`）验证，无需任何 npm 依赖。

## 运行环境

- Node.js >= 22.6（利用 `--experimental-strip-types` 直接运行 TypeScript，零构建）

```bash
npm test    # 运行全部测试（node:test + --expose-gc 用于泄漏断言）
npm run demo  # 运行样例 examples/demo.ts
```

## 模块结构

| 文件 | 职责 |
| --- | --- |
| `src/varint.ts` | Varint 7 位压缩编解码（10 字节上限保护）、ZigZag 32/64 位转换 |
| `src/reader.ts` | 边界安全的 `WireReader`：tag/varint/fixed32/fixed64/float/double/length-delimited/未知字段跳过 |
| `src/writer.ts` | 可增长的 `WireWriter` |
| `src/schema.ts` | 反射模式校验引擎 `MessageSchema`：字段描述符驱动编解码，支持嵌套消息与 packed repeated |
| `src/sparse.ts` | `SparseVector` 稀疏向量：map/dense 容器自适应转换（带迟滞）、RLE+varint 压缩、`AsyncMutex` 并发安全包装 |
| `src/errors.ts` | `ProtobufDecodeError`（携带字节偏移）/ `ProtobufEncodeError` |

## 关键设计

### 安全越界防护
所有读取先校验剩余字节，截断时抛出携带偏移量的 `ProtobufDecodeError`，
绝不产生未定义行为；varint 超过 10 字节判定为溢出畸形；字段号 0 与保留
wire type（3/4/6/7）直接拒绝。

### 反射模式校验
`MessageSchema` 以字段描述符驱动：解码时校验 wire type 匹配、packed 段完整
消费、嵌套消息边界闭合、无尾部垃圾字节；未知字段按 wire type 安全跳过。

### 稀疏优化器
- **自适应容器**：密度 ≥ 0.5 且跨度 ≤ 2^20 时切到 dense 数组，密度 < 0.25
  时回落 Map（迟滞防抖动）；超大索引（≥ 2^32）自动保持 Map，避免病态数组。
- **压缩**：游程编码（RLE）+ varint 索引增量 + ZigZag 值编码，稀疏间隙零开销。
- **并发安全**：同步单操作天然原子；`ConcurrentSparseVector` 经 `AsyncMutex`
  串行化批量变更。
- **无泄漏**：`clear()` 释放全部内部容器；测试在 `--expose-gc` 下断言堆内存
  增长低于阈值。

## 测试覆盖（31 项）

- varint/zigzag 边界值（0、127、128、2^32-1、2^64-1、INT32/64_MIN/MAX）、截断与溢出畸形
- fixed32/64、float/double 往返；所有读取点的越界安全失败
- 三层嵌套消息 + packed/unpacked repeated 往返；手工拼装裸二进制流还原
- 截断扫描：对编码结果的所有前缀，断言只会成功或抛 `ProtobufDecodeError`
- 未知字段跳过、wire type 不匹配、尾部垃圾、空消息
- 稀疏容器双向自适应转换与迟滞、边界索引、非法输入、压缩往返、压缩流截断防护
- 32 路并发批量写入/删除与串行结果一致；clear 后 GC 堆增长 < 8MB
