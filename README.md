# ssidb — 只读分片 uint32 集合文件 + 流式分页查询

检索服务分页返回压缩 ID 集合的实现：只读分片文件格式、AND/OR 表达式查询、
绑定输入版本的游标、按租户配额的流式多路合并，以及独立验证器。

## 文件格式（`src/format.ts`）

小端二进制。ID 为 uint32，集合内严格递增。

- **头部（76B）**：magic `SSIDB001`、setId（32B）、version（32B）、分片数。
- **有序目录（每片 56B）**：半开区间 `[rangeStart u32, rangeEnd u64)`、
  片内条数、片段偏移/长度、片段的 sha256 摘要。分片按区间升序、互不重叠。
- **片段**：块数 + 若干块；每块 ≤128 个 ID，存 base + LEB128 delta。
  块是最小解码单元（满块解码后 512 字节），支持超大稀疏间距（delta 可达 2^32-1）。

## 查询引擎（`src/engine.ts`）

- 表达式 `and(...)/or(...)/集合名`（`src/expr.ts`），以流式多路合并求值：
  OR 做 k 路归并去重，AND 做交集推进；每个叶子集合同一时刻至多钉住一个
  解码块，**绝不预先展开整个结果**。
- **游标**（`src/cursor.ts`）绑定：表达式哈希（操作数序无关）、所有输入的
  `(setId, version)`、页大小 k、最后返回的 ID；带 sha256 完整性标签。
  同一游标重复请求返回同一页；取消后凭游标重读，不漏不重。
- **配额**：每租户 Session 限制并发钉住的已解码字节；额度放不下单个最小块时
  抛出 `QuotaExceededError` 明确拒绝。块缓存（`src/cache.ts`）全局共享，
  配额按租户独立记账，页统计报告峰值解码字节。

## 独立验证器（`src/validate.ts`）

- `validateFile`：目录覆盖（有序、不重叠、半开）、逐片条数与 sha256 摘要、
  ID 严格递增且落在片区间内。
- `validateCursorBinding`：游标完整性与表达式/版本绑定。
- `referenceEval` + `validatePages`：全量展开集合做集合运算参考实现，
  与分页输出逐位对照。

## 构建与测试

环境无网络、无 tsc；用本机 Bun 把 TypeScript 转译为 JS 后用 Node 运行：

```sh
npm test          # 构建 + 运行全部 Node 测试（test/*.test.ts）
npm run build     # 仅构建 dist/cli.js 与 dist-test/
```

## CLI

```sh
node dist/cli.js build --set A --version v1 --out a.ssidb --ids "1,2,3" --shard-size 1000
node dist/cli.js query --expr "or(A,and(B,C))" --set A=a.ssidb --set B=b.ssidb \
     --k 100 --quota 65536 --tenant t1 [--cursor C] [--all]
node dist/cli.js validate --file a.ssidb
node dist/cli.js validate --expr "or(A,and(B,C))" --set A=a.ssidb ... --k 100
```

`query` 每页输出一行 JSON：`ids`、`nextCursor`、`stats`（含 `peakDecodedBytes`、
解码块数、缓存命中）。`validate` 对照全量参考实现校验分页结果。

## 测试覆盖（30 项）

两页间切换输入版本被拒、坏条数、交换片段、超大稀疏 ID 间距（含 2^32-1）、
k=0 非法页大小、多租户共享缓存但配额独立、空片、连续重复页请求、
取消后重读、配额不足单块明确拒绝、流式内存上界等。
