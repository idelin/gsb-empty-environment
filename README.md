# JWS crit与未编码载荷

攻击者在 JWS 头里塞验证方不认识的 crit 扩展，或用 b64=false 让载荷含句点后改变分段。为TypeScript验证库写可导入的 JWS 头准入组件：crit 须是非空数组，所列名字须出现在头里、不属 RFC 7515 已注册名，且在调用方认可集合内，否则拒绝；b64=false 仅在 crit 含 b64 时有效，此时紧凑序列化的载荷不得含 . 且签名输入为头原文+.+载荷原字节；头里同名成员重复拒绝。测未知扩展、缺 crit 的 b64、含句点载荷。

## 组件

`src/jws-header.ts` 是零依赖的可导入组件，从 `index.ts` 重新导出：

- `admitProtectedHeader(segment, { allowedCrit })`：解析并准入 JWS 保护头。
- `admitCompactJws(compact, { allowedCrit })`：准入紧凑序列化并返回 `signingInput`、`payloadBytes`、`signatureSegment`。
- `JwsHeaderError`（含 `code`）、`DEFAULT_ALLOWED_CRIT`、base64url 工具。

准入规则：

- `crit` 必须是非空字符串数组；条目须实际出现在头中、不得是 RFC 7515 §4.1 已注册名、且在调用方 `allowedCrit` 集合内，否则按对应错误码拒绝。
- `b64=false` 仅在 `crit` 含 `"b64"` 时有效（RFC 7797）；此时载荷按原字节处理，紧凑序列化载荷段不得含 `.`，签名输入为 `头原文 + . + 载荷原字节`。
- 头 JSON 同名成员重复直接拒绝（自实现严格解析，不依赖 `JSON.parse` 的静默覆盖）。

```ts
import { admitCompactJws } from './index.ts';

const admitted = admitCompactJws(compact, { allowedCrit: new Set(['b64']) });
// admitted.signingInput 送入 HMAC/ECDSA 验证；admitted.signatureSegment 为签名段
```

## 测试

```sh
npm test
```

覆盖未知 crit 扩展、缺 `crit` 的 `b64=false`、含句点载荷、重复头成员、已注册名、空/非数组 crit、签名输入字节构造等场景。
