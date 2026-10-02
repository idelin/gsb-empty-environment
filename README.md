# proto-wire-metrics

零外部依赖的 Protocol Buffers 二进制 Wire-format 解析器 + 反射模式校验引擎 + 细粒度度量监控（TypeScript，Node.js ≥ 23.6 原生类型擦除运行，无需编译步骤）。

## 功能

**Wire-format 编解码（`src/wire/`）**
- Varint 7 位压缩编码（64 位无损，BigInt；10 字节上限，溢出报 `ERR_VARINT_OVERFLOW`）
- ZigZag 有符号整数转换（sint32/sint64）
- Fixed32/Fixed64、sfixed、float/double（小端）
- Length-delimited：字符串（UTF-8 校验）、bytes、嵌套消息
- `BufferReader`：全程边界检查游标，任何截断都报 `ERR_TRUNCATED`（带偏移与字段号），绝不越界读取；`release()` 释放缓冲区引用

**反射模式解码与校验（`src/reflect/`）**
- 描述符驱动的解码：嵌套消息（深度限制防栈耗尽）、Packed/非 Packed Repeated 均兼容、未知字段按 wire type 安全跳过
- 校验引擎：required、自定义校验器（repeated 逐元素）、嵌套消息递归（点分路径定位问题）、深度限制

**度量监控（`src/metrics/` + `src/engine.ts`）**
- 高精度时延直方图：`process.hrtime.bigint()` 纳秒采样，指数分桶，p50/p90/p95/p99/min/max/mean
- 命中率：schema 编译缓存 hit/miss/ratio
- 错误码计数器：每种 `DecodeError` 码独立计数；校验问题按规则分类计数
- 内部状态 gauge：活跃解码数（并发平衡性可断言）、消息/字节总量
- 聚合导出：`exportJson`（结构化快照）与 `exportText`（Prometheus 风格文本）
- 所有度量操作为同步无 await，异步并发下读-改-写不会交错，总量精确

## 运行

```bash
npm test    # node --test（40 个测试）
npm run demo
```

## 测试覆盖（`test/`）

- `varint.test.ts`：边界值往返、规范编码向量、溢出、截断、ZigZag 已知向量、fixed 越界防护、reader 窗口越界、release 泄漏防护
- `decoder.test.ts`：多层嵌套 + packed repeated 还原、非 packed 兼容、未知字段跳过、空流合法、**逐字节截断扫描**（每个前缀要么合法短消息要么有界 `DecodeError`，绝不崩溃）、长度前缀截断定位字段号、wire type 不匹配、0 号字段、非法 UTF-8、深度限制、packed 体截断
- `validator.test.ts`：required、空 repeated、逐元素自定义校验、嵌套点分路径、深度限制
- `metrics.test.ts`：直方图分桶边界（0/1/2 的幂/溢出桶）、空快照、百分位范围、负值钳制、命中率/计数器/gauge 聚合、导出格式、reset、**512 路并发解码计数精确且 gauge 归零**、**无泄漏断言**（active handles/requests 不增长、输入缓冲区不被保留）
