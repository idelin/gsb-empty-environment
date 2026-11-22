离线阅读器可能同时持有作者日志的两段签名后缀，中继承诺只发摘要，缺失前缀不能当作作者从未写过。为 TypeScript 复制宿主交付可导入的摄取包。每条签名记录绑定作者永久 ID、公钥代次、按字节规范编码的正文、序号和前一摘要。轮换由旧钥签发且新钥确认，撤销另有独立见证门槛。给定多个检查点和乱序增量，对每个作者维护所有可能头、已经证实的共同前缀和需要补证的缺口；同序号不同合法签名即隔离分歧之后的内容，已经确定的共同前缀仍可读。轮换签名若只出现在冲突的一支，不得用来认可另一支的新记录。允许受信调解证书选择一支，证书必须命名全部已知冲突头并绑定观察范围；后来发现一支早已有签名的新头时，不能沿用覆盖不全的旧调解决定。提供小型证明给只持检查点的读端，明确哪些消息是确定、隔离或未知，支持无损压缩无分叉前缀。以全记录图解释器验证多次分叉、先到后代、同钥别名、恢复旧快照和 64 位序号边界。

## 包结构

- `src/encoding.ts` — 字节规范编码（u32/u64 大端、长度前缀、SHA-256 摘要），`MAX_SEQ = 2^64 - 1`。
- `src/keys.ts` — Ed25519 密钥生成、签名、验证；密钥 ID 为公钥 DER 的摘要。
- `src/records.ts` — 签名记录：绑定作者永久 ID、公钥代次、规范编码正文、序号（bigint）、前一摘要。
- `src/certs.ts` — 轮换证书（旧钥签发 + 新钥确认，锚定分支记录）、撤销证书（独立见证 m-of-n 门槛）、中继检查点、调解证书（命名全部已知冲突头并绑定观察序号范围）。
- `src/author-log.ts` — 每作者派生视图：所有可能头、已证实共同前缀（`commonPrefix` + `unknownPrefixBelow`，缺失前缀不当作从未写过）、待补证缺口、分叉隔离（同序号同前驱不同摘要即 equivocation，分歧之后内容隔离，共同前缀仍可读）、轮换分支作用域检查、调解适用性判定。
- `src/host.ts` — `IngestHost`：`ingestRecord / ingestCheckpoint / ingestRotation / ingestRevocation / ingestMediation`，乱序增量（未知代次的记录挂起待证书到达后重试），`snapshot / restore / mergeSnapshot`（合并旧快照不回退新状态）。
- `src/proof.ts` — 面向只持检查点读端的小型证明：`buildProof` 标注确定/隔离/未知，`compressProof` 将无分叉确定前缀无损压缩为单区间（链式摘要承诺，持记录可完全还原），`expandRanges` 还原。
- `src/graph.ts` — 全记录图解释器，作为增量摄取的验证基准。

## 用法

```ts
import { IngestHost, generateKey, makeRecord, genesisData, utf8 } from './src/index.ts';

const kp = generateKey();
const host = new IngestHost({ authors: [{ id: 'author-1', gen0Key: kp.publicDer }] });
host.ingestRecord(makeRecord(kp, genesisData('author-1', 0, utf8('hello'))));
const view = host.view('author-1'); // heads / commonPrefix / gaps / quarantined / readable
```

```bash
npm test   # node --experimental-strip-types --test
```
