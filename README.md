# wireshard

零外部依赖的 TypeScript 组件库，包含两部分：

1. **二进制紧凑变长编解码器 + 反射模式校验引擎**(`src/varint.ts`、`src/wire.ts`、`src/schema.ts`)
   - 纯手工解析 Protobuf 二进制 wire format,无任何第三方依赖
   - Varint 7 位压缩编码、ZigZag 有符号转换、Fixed32/64(小端)、Length-delimited 嵌套消息
   - Packed repeated 数组、未知字段安全跳过、严格越界防护(截断即抛 `TruncatedFieldError`)
   - 反射式 Schema 描述驱动解码/编码/校验,校验聚合全部违规项并带完整字段路径

2. **分层分片与局部性感知路由引擎**(`src/shard.ts`)
   - 三级拓扑:region → zone → node
   - 固定数量虚拟分片(virtual shards)+ 加权 Rendezvous 哈希分配,节点增删只迁移少量 key
   - 副本分层多样性:优先跨 zone、再跨 region 放置副本,单 zone/region 故障不丢全部副本
   - 路由按局部性距离排序(同 zone < 同 region < 远端),同距离按在途负载决胜,显著降低跨节点通信
   - 并发安全:拓扑变更经异步互斥锁串行化,发布不可变版本化快照,读路由零锁且永不观测到撕裂状态
   - `dispose()` 释放全部内部状态,无句柄/计时器泄漏

## 环境要求

Node.js >= 22.6(利用 `--experimental-strip-types` 直接运行 TypeScript,无需编译步骤)。

## 运行

```bash
npm test     # Node.js 内置测试运行器(node:test)
npm run demo # 端到端样例:编码/解码/校验/截断防护 + 局部性路由统计
```

## 目录结构

```
src/varint.ts   Varint / ZigZag / Fixed32 / Fixed64 基础编解码
src/wire.ts     带严格边界检查的 WireReader / WireWriter
src/schema.ts   反射 Schema 解码、编码与校验引擎
src/shard.ts    分层分片 + 局部性感知路由引擎
test/           node:test 测试(边界条件、并发安全、无泄漏断言)
examples/demo.ts 运行样例
```

## 错误模型

| 错误类型 | 触发条件 |
| --- | --- |
| `TruncatedFieldError` | 缓冲区在任何字段中途结束(varint/fixed/length-delimited) |
| `VarintOverflowError` | varint 编码超过 64 位(>10 字节或第 10 字节超 1 位) |
| `MalformedTagError` | 字段号 0 或非法 wire type(3/4/6/7) |
| `SchemaError` | Schema 定义矛盾或 wire type 与声明类型不匹配 |
| `ValidationError` | 反射校验失败,聚合全部违规路径 |
| `TopologyError` | 空拓扑路由、无健康副本、非法节点/配置、已 dispose |
