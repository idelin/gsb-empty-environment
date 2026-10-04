# Protobuf Wire-Format Codec + Locality-Aware Sharding Router

零外部依赖的 TypeScript 实现，包含两大组件：

1. **二进制紧凑变长编解码器与反射模式校验引擎**（`src/wire/`、`src/schema.ts`）
   - 纯手工解析 Protocol Buffers 二进制 wire format，无任何第三方依赖
   - Varint 7 位压缩编解码（64 位，10 字节上限守卫）、ZigZag 有符号整数转换
   - Fixed32 / Fixed64（小端）、嵌套 Length-delimited 消息（深度限制防护）
   - Packed Repeated 数组（varint / fixed32 / fixed64）
   - 反射模式校验：schema 注册校验（字段号冲突、保留段 19000-19999、递归环）、
     wire-type 兼容性检查、required 字段检查、严格/宽松未知字段策略
   - 安全越界防护：所有读取先校验剩余长度，截断输入一律抛出
     `ProtobufDecodeError`（携带偏移量），绝不产生裸越界访问

2. **分层分片与局部性感知路由引擎**（`src/sharding/`）
   - 固定数量虚拟分片（默认 1024），加权 Rendezvous(HRW) 哈希放置：
     节点增删只迁移 ~1/N 分片，无全量 rehash
   - 层级拓扑 Region → Zone → Node，副本放置带反亲和：
     优先跨 Zone、再跨 Region 分散副本，提升容灾性
   - 局部性感知路由：按调用方位置对副本排序（同 Zone > 同 Region > 远端），
     内置指标统计本地命中率与跨节点跳数，大幅降低跨节点通信
   - 并发安全：拓扑变更经异步互斥锁串行化，路由查询永观察不到中间态
   - 无泄漏：节点移除后从所有索引彻底清除（含 GC/WeakRef 探针测试）

## 环境要求

Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译、无需任何依赖安装）。
Node 22.6+ 可加 `--experimental-strip-types` 运行。

## 运行

```bash
npm test    # node --expose-gc --test 'tests/*.test.ts'
npm run demo
```

## 目录结构

```
src/wire/varint.ts    Varint/ZigZag/64 位符号重解释
src/wire/reader.ts    边界检查游标 + tag/wire-type 解析
src/wire/writer.ts    编码器（测试夹具与示例）
src/wire/decoder.ts   无 schema 原始字段解码 + packed 数组解码
src/wire/errors.ts    ProtobufDecodeError / SchemaValidationError
src/schema.ts         反射模式校验引擎（schema 注册 + 类型化解码）
src/sharding/hash.ts  FNV-1a 哈希
src/sharding/engine.ts 分层分片 + 局部性感知路由
tests/                Node 内置 test runner 测试（47 个用例）
examples/demo.ts      端到端运行样例
```

## 测试结果

```
ℹ tests 47
ℹ pass 47
ℹ fail 0
```

覆盖：varint 边界值（0/127/128/2^64-1）与截断逐字节前缀、ZigZag 极值、
多层嵌套消息、packed repeated、逐字节截断的越界防护断言、schema 校验全部
非法形态、空拓扑/单节点/副本数大于节点数、副本反亲和、最小迁移、局部性排序、
指标一致性、50 节点并发增删 + 200 并发路由的一致性、幂等删除、
全量拆除后的泄漏检查（内部索引清空 + WeakRef GC 探针）。
