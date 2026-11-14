# 重复 JSON 键安全网关组件

API 网关中的 WAF 和下游框架如果各自解析原始 JSON，就可能对重复键采用不同取值
（例如 WAF 取第一个 `role`，框架取最后一个 `role`），从而产生鉴权绕过。

本包提供可导入的 TypeScript 请求规范化与准入组件：

- 原始 JSON 只由一个严格解析器解析一次。
- 解析结果是一棵不可变的规范树，每个节点都带 UTF-8 字节区间 `start`/`end`
  （以及 `range` 别名；`start` 含、`end` 不含）。
- 鉴权键重复时直接拒绝，包括嵌套重复、`\u` 转义同名键和大小写变体。
- 非鉴权重复键按 `first` / `last` 折叠，另支持 `reject` 策略。
- 签名摘要只覆盖折叠后的规范 JSON，不覆盖含重复键的原始字节。
- WAF 与路由共享同一个规范化对象；下游接口拒绝接收原始请求体。
- 数字、字符串转义、代理对和嵌套深度使用同一套解析规则。

## 快速使用

```ts
import {
  admitRequest,
  normalizeRequest,
  signNormalized,
} from "./index.ts";

const body =
  '{"role":"user","tenantId":"t-1","scope":"orders:read","trace":"a"}';

// 一次规范化。
const normalized = normalizeRequest(body);

// 签名应针对折叠后的规范 JSON 计算。
const signature = signNormalized(normalized);

// 一次准入：WAF 和路由都从同一个 normalized 对象做决策。
const decision = admitRequest(body, {
  "x-json-signature": `sha256=${signature}`,
});

if (!decision.allow) {
  // decision.reasons 包含稳定错误码、路径和重复键字节区间。
  throw new Error(JSON.stringify(decision.reasons));
}

// 下游只能使用这个值，不得再次 JSON.parse 原始 body。
const routedBody = decision.router.document?.value;
```

攻击样例：

```ts
const attack = '{"role":"user","role":"admin"}';
const decision = admitRequest(attack);

decision.allow;                    // false
decision.waf.allow;                // false
decision.router.allow;             // false
decision.consistent;               // true
decision.reasons[0].code;          // "PROTECTED_KEY_DUPLICATE"
```

## 规范化结果

`normalizeRequest(body, options)` 返回冻结对象：

```ts
interface NormalizedRequest {
  document: { root: CanonicalNode; node: CanonicalNode; value: JsonValue };
  root: CanonicalNode;             // document.root
  canonicalRoot: CanonicalNode;    // 同上
  node: CanonicalNode;             // 同上
  value: JsonValue;                // document.value
  duplicates: DuplicateKeyRecord[];
  duplicateKeys: DuplicateKeyRecord[];
  protectedDuplicates: DuplicateKeyRecord[];
  auth: Record<string, JsonValue>; // 仅顶层鉴权键，键名已规范化
  authIssues: AuthKeyIssue[];
  canonicalJson: string;
  signaturePayload: string;        // 等于 canonicalJson
  bodyRange: { start: 0; end: number };
  digest: string;                  // SHA-256(canonicalJson UTF-8)
  policy: "first" | "last" | "reject";
  stats: {
    bytes: number;
    nodes: number;
    maxDepthSeen: number;
    duplicateGroups: number;
    protectedDuplicateGroups: number;
  };
}
```

规范树节点示例：

```ts
{
  kind: "number",
  start: 42,                 // 原始 body 的 UTF-8 字节偏移
  end: 58,
  range: { start: 42, end: 58 },
  path: "$.id",
  raw: "9007199254740993",   // 精确源文本
  canonical: "9.007199254740993e15"
}
```

重复键记录包含 JSONPath 风格路径、是否属于鉴权键、策略、解析方式，以及每次出现
的 key/value 字节区间：

```ts
{
  path: "$.role",
  key: "role",
  keys: ["role"],
  protected: true,
  caseVariants: false,
  policy: "last",
  resolution: "reject",
  occurrences: [{
    key: "role",
    rawKey: "\"role\"",
    keyRange: { start: 1, end: 7 },
    valueRange: { start: 8, end: 14 },
    index: 0,
    selected: false
  }, /* ... */]
}
```

## 准入结果

```ts
interface AdmissionDecision {
  ok: boolean;
  allow: boolean;
  decision: "allow" | "deny";
  reasons: AdmissionReason[];
  normalized: NormalizedRequest | null;
  digest: string | null;
  waf: LayerDecision;
  router: LayerDecision;
  consistent: boolean;
}
```

`waf.normalized === router.normalized`，两层永远基于同一棵规范树。解析失败时
`normalized` 为 `null`，两层得到相同的拒绝原因。`consistent` 为真还必须满足：

```ts
waf.allow === router.allow &&
waf.normalized === router.normalized &&
waf.document === router.document &&
waf.auth === router.auth &&
waf.reasons === router.reasons &&
waf.digest === router.digest
```

### 下游只接收规范化对象

```ts
import { evaluateWaf, routeRequest } from "./index.ts";

const waf = evaluateWaf(normalized);
const route = routeRequest(normalized);
```

`evaluateWaf`、`routeRequest`、`admitNormalized` 和 `normalizedBody` 都带运行时
品牌校验。传入字符串、Buffer 或已解析普通对象会抛出 `TypeError`，因此不能借这些
接口重新解析原始体。

## 鉴权键与重复策略

默认鉴权键包括：

```ts
role, roles, tenantId, tenant_id, scope, scopes,
permissions, permission, userId, user_id,
sub, iss, aud, groups, claims, authorization
```

默认按 ASCII 大小写不敏感比较，因此 `role` / `ROLE` 会被视为同一鉴权键并拒绝。
可通过 `protectedKeys` 和 `caseInsensitiveProtectedKeys` 调整。默认
`strictAuthKeyCase: true`，单独出现的非规范拼写（如 `{"Role":"admin"}`）也会被
拒绝。

非鉴权重复键策略：

```ts
normalizeRequest(body, { duplicatePolicy: "first" });
normalizeRequest(body, { duplicatePolicy: "last" });   // 默认
normalizeRequest(body, { duplicatePolicy: "reject" }); // 准入时拒绝
```

鉴权键重复不受该策略影响，始终拒绝。

## 数字与 Unicode

- 数字不经过 `number` 精度转换。规范树保留 `raw`，规范形式使用精确有效数字和
  符号化指数，例如 `9007199254740993` 不会变成 `9007199254740992`。
- 数值等价的拼写会得到同一摘要，例如 `100`、`1.00`、`1e2` 都规范为 `1e2`。
- `document.value` 默认使用 `safe` 模式：可精确往返的数值返回 JS `number`，
  超出安全范围或溢出的值返回精确源字符串。可用 `numberMode: "exact"` 全部返回
  源字符串，或显式选择 `"number"` / `"bigint"`。
- `\uXXXX` 在键比较前解码；`\uD83D\uDE00` 与字面量 `😀` 是同一个键。
- 孤立代理项、非法 UTF-8、非法转义和非法数字语法都会被拒绝。
- 字节区间始终是 UTF-8 偏移，不受 JavaScript UTF-16 索引影响。

## 深度与大小限制

默认限制：

- `maxDepth: 64`：根对象/数组深度为 1，超过时返回 `DEPTH_EXCEEDED`。
- `maxBytes: 8 * 1024 * 1024`：超过时返回 `BODY_TOO_LARGE`。

两者均可在 `normalizeRequest` / `admitRequest` / `createRequestGate` 中覆盖。

## 签名

签名 payload 是折叠、键排序、数值规范化和字符串规范化后的 `canonicalJson`。
这样可以保证签名覆盖的正是 WAF 和路由看到的内容：

```ts
import {
  signNormalized,
  verifyNormalizedSignature,
} from "./index.ts";

const sha = signNormalized(normalized);
const hmac = signNormalized(normalized, secret);

verifyNormalizedSignature(normalized, `sha256=${sha}`);
```

准入头默认是 `x-json-signature`，支持 `sha256=<hex>` 和
`hmac-sha256=<hex>` 前缀：

```ts
admitRequest(body, headers, {
  signature: {
    header: "x-json-signature",
    secret,
    required: true,
  },
});
```

如果请求带签名头但未配置 `required`，仍会校验该头；头缺失时才默认放行。
对原始字节计算旧式签名不会通过，因为重复键折叠后的规范摘要已经改变。

## Node 中间件

```ts
import { createAdmissionMiddleware } from "./index.ts";

app.use(createAdmissionMiddleware({
  protectedKeys: ["role", "tenantId", "scope"],
  signature: { secret: process.env.JSON_HMAC_KEY, required: true },
}));
```

中间件要求 `req.rawBody` 仍是原始字符串/Buffer（或 `req.body` 尚未解析）。
成功后写入：

- `req.normalizedRequest`
- `req.admission`
- `req.body = req.normalizedRequest.document.value`

若 `req.body` 已经是对象，则返回 `RAW_BODY_UNAVAILABLE`，避免对已丢失重复键信息
的数据做第二次、不可信解析。

## 测试

```bash
npm test
```

测试覆盖：首尾不同鉴权值、嵌套重复、转义同名键、大小写键、超深嵌套、合法单值
请求、精确大数、代理对、UTF-8 字节区间，以及签名与折叠结果一致性。
