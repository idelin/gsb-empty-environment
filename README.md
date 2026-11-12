# CPace ISK 派生

供 TypeScript 登录握手调用的 CPace 中间会话密钥（ISK）派生包。实现遵循
`lv_cat(DSI_ISK, sid, K) || transcript(messages)`：

- 每段使用 CPace 的 LEB128 `prepend_len` 编码，避免拼接歧义。
- 对称场景使用 `o_cat`：两端消息按字节字典序排列，并前置 `oc` 域标记。
- 发起方—响应方场景固定按 `initiatorMessage`、`responderMessage` 顺序。
- `H.hash(...)` 默认 SHA-512，也可传入 Node 哈希名或同步哈希函数。
- 共享点 `K` 为单位元时抛出 `IdentityPointError`。
- 成功的 ISK 派生会把 `sid` 标记为已完成；再次使用该 `sid` 会抛出
  `SidReuseError`。失败的派生不会占用 `sid`。

## 登录握手用法

```ts
import {
  ISKDeriver,
  withISKSuffix,
} from "./index.ts";

const deriver = new ISKDeriver({ hash: "sha512" });

const isk = deriver.derive({
  mode: "initiator-responder",
  // CPace cipher suite 的 G.DSI 通常还需要追加 "_ISK"。
  domainSeparator: withISKSuffix("CPaceRistretto255"),
  sid,
  sharedPoint: K,
  initiatorMessage: { publicShare: Ya, associatedData: ADa },
  responderMessage: { publicShare: Yb, associatedData: ADb },
});
```

对称场景把两个完整消息放进 `messages` 二元组；实现会自动做字典序规范化：

```ts
const isk = deriver.derive({
  mode: "symmetric",
  domainSeparator: withISKSuffix("CPaceRistretto255"),
  sid,
  sharedPoint: K,
  messages: [messageA, messageB],
});
```

`messageA`/`messageB` 可以是字符串、`Uint8Array`，或
`{ publicShare, associatedData? }`。`domainSeparator` 会按传入字节原样使用；
标准套件请通过 `withISKSuffix(G.DSI)` 构造完整的 `DSI_ISK`。

## 安全边界

- `ISKDeriver` 的 `derive` 是登录握手的完成点。一个 SDK 实例或共享的
  `InMemoryCompletedSidStore` 会拒绝已完成 `sid` 的任意新派生，包括消息或
  `K` 不同的重放。多进程部署应把该接口替换为持久化/原子存储。
- 无状态的 `deriveISK` 只负责密码学转录，不提供重放保护；握手入口应使用
  `ISKDeriver`。
- 默认单位元检查拒绝全零编码，适用于 X25519/Ristretto255 等常见套件。其他
  群应通过构造参数 `identityElement` 或 `isIdentityElement` 注入其编码后的
  单位元判定。
- K 必须来自经过群验证的标量乘法；本包不替代 CPace 的曲线点解析和子群检查。

## 验证

```sh
npm test
```

测试覆盖 CPace `lv_cat`/LEB128 已知向量与 127/128/16383/16384 边界、消息拆分
歧义、对称/角色顺序互换、`sid` 重放、共享注册表以及默认和自定义单位元拒绝。
