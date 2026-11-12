# nostr-nip01-validate

供 TypeScript Nostr 中继**事件入库环节**调用的 NIP-01 校验包，零运行时第三方依赖（只用
`node:crypto` 做 SHA-256），Schnorr (BIP-340) 验签由调用方注入。

## 解决的两类攻击

1. **等价 JSON 转义伪造 id**：NIP-01 的事件 id 绑定的是「固定数组的规范化序列化」的哈希。
   攻击者可以用 `\u000a` 代替 `\n`、`\/` 代替 `/` 等非规范转义，为相同逻辑内容算出另一个
   合法哈希。本包**不信任线上报文里的 id 或原始字节**，而是用自带的规范化序列化器重算 id，
   非规范转义算出的 id 一律 `BAD_ID` 拒绝。
2. **旧可替换事件重放覆盖新状态**：可替换/可寻址事件按固定存储键去重，入库决胜规则为
   `created_at` 较大者胜，时间戳相同则 id 字典序较小者胜，且与到达顺序无关。

## 规范化序列化规则

id = `sha256` 小写十六进制，哈希输入为以下数组的**无空白** JSON 序列化：

```
[0, pubkey, created_at, kind, tags, content]
```

字符串（含 `content`、`pubkey` 与 tag 元素）只转义这 7 个字符，其余字符（包括 `/`、
U+0000 等控制字符）原样输出：

| 字符 | 转义 |
| --- | --- |
| 换行 `\n` | `\\n` |
| 双引号 `"` | `\\"` |
| 反斜杠 `\` | `\\\\` |
| 回车 `\r` | `\\r` |
| 制表 `\t` | `\\t` |
| 退格 `\b` | `\\b` |
| 换页 `\f` | `\\f` |

## 校验流程（`validateEvent`）

1. 报文必须是对象（非数组、非 null）。
2. `pubkey`：恰好 64 位小写十六进制；`sig`：恰好 128 位小写十六进制；`id`：64 位小写十六进制。
3. `created_at`、`kind`：非负安全整数；`tags`：字符串数组的数组；`content`：字符串。
4. 用规范化序列化重算 id，与线上 id 做**常量时间**比较，不符则 `BAD_ID`（此时不调用验签）。
5. `created_at > now + futureLimitSeconds` 则 `FUTURE_TIMESTAMP`；`now` 可注入，默认当前秒。
6. 调用注入的 `verifySchnorr(id, pubkey, sig)`：返回非 `true` 为 `BAD_SIG`；抛错为
   `SIG_VERIFIER_ERROR`，支持异步实现。

结果以判别联合返回（不抛业务异常）：`{ ok: true, event }` 或
`{ ok: false, code, reason }`；另有 `assertValidEvent` 抛异常版本。

## 可替换事件分类与决胜

| kind | 分类 | 存储键 |
| --- | --- | --- |
| 0、3、10000–19999 | replaceable | `r:<pubkey>:<kind>` |
| 30000–39999 | parameterized replaceable | `p:<pubkey>:<kind>:<d 标签值>` |
| 20000–29999 | ephemeral | 不入索引（返回 `ephemeral`） |
| 其他 | regular | 不去重，直接追加 |

- 可寻址事件取第一个 `d` 标签的值；没有 `d` 标签（或无第二个元素）时为空字符串键。
- 同键冲突：较新 `created_at` 胜；相同则 id 字典序较小者胜；完全相同的事件记为 `duplicate`。

## 用法

```ts
import {
  validateEvent,
  ReplaceableEventIndex,
  type SchnorrVerifier,
} from 'nostr-nip01-validate';

// 由调用方提供，例如 @noble/curves 的 schnorr.verify
const verifySchnorr: SchnorrVerifier = async (id, pubkey, sig) => {
  // return schnorr.verify(sig, id, pubkey);
  return true;
};

const index = new ReplaceableEventIndex();

async function ingest(raw: unknown) {
  const result = await validateEvent(raw, {
    verifySchnorr,
    futureLimitSeconds: 900, // 允许最多 15 分钟时钟漂移
  });
  if (!result.ok) {
    return { admitted: false, code: result.code };
  }

  const decision = index.ingest(result.event);
  // decision.action: 'store' | 'replace' | 'duplicate' | 'reject_stale' | 'ephemeral'
  if (decision.action === 'reject_stale') {
    return { admitted: false, code: 'REPLAY_STALE' };
  }
  if (decision.action === 'ephemeral') {
    return { admitted: true, persisted: false };
  }
  return { admitted: true, persisted: true, key: decision.key };
}
```

## 目录与测试

```
src/
  canonical.ts   规范化 JSON 序列化（受限转义集）
  hash.ts        sha256、id 计算、常量时间十六进制比较
  validate.ts    NIP-01 入库校验主流程
  relay.ts       种类分类、存储键、替换决胜、内存索引
tests/           node:test 测试（含转义伪造、重放、同时间戳决胜、未来时间）
```

运行测试（Node >= 22.6，零依赖、无需安装）：

```sh
npm test
```
