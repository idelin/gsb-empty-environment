# GNAP交互结束哈希校验

攻击者把自己的 interact_ref 塞进受害者的回调，想把授权结果绑到受害者会话。为TypeScript客户端写可导入的 GNAP 交互结束校验组件：回调后算 base64url(SHA-256(client_nonce+LF+as_nonce+LF+interact_ref+LF+授权请求URL))，与 hash 参数常量时间比较，一致才用 interact_ref 续期；每个事务只收一次回调，且须出现在登记的浏览器会话里。测伪造 ref、跨会话、重复回调。

## 用法

组件只依赖 Node 内置模块（`node:crypto`），从包入口直接导入：

```ts
import {
  GnapInteractionVerifier,
  GnapVerificationError,
} from "./index.ts";

const verifier = new GnapInteractionVerifier();

// 1. 发起授权请求后登记事务，browserSessionId 取自已认证的会话/Cookie。
verifier.register(transactionId, {
  clientNonce,
  asNonce,
  browserSessionId,
  authorizationRequestUrl,
});

// 2. 浏览器回调到达时，从查询串取 interact_ref/hash，会话 ID 仍取自服务端会话。
try {
  const { interactRef } = verifier.verifyCallback(transactionId, {
    interactRef: query.interact_ref,
    hash: query.hash,
    browserSessionId: session.id,
  });
  // 仅当校验通过时，才允许用 interactRef 向 AS 续期/轮询。
} catch (error) {
  if (error instanceof GnapVerificationError) {
    // error.code: INVALID_HASH | CROSS_SESSION_CALLBACK |
    //             DUPLICATE_CALLBACK | UNKNOWN_TRANSACTION | INVALID_INPUT
  }
}

// 3. 续期前只从组件取已校验的引用，不要直接信任回调参数。
const safeRef = verifier.getContinuationInteractRef(transactionId);
```

## 安全保证

- 哈希按 RFC 9724 以单个 LF 连接四段输入，SHA-256 后输出无填充 base64url。
- 哈希比较使用 `crypto.timingSafeEqual`（等长时恒定时间），不泄露前缀信息。
- 校验顺序：未知事务 → 重复回调 → 跨会话 → 哈希不匹配；失败不修改事务状态，伪造回调无法制造拒绝服务。
- 每个事务仅接受一次成功回调，且回调必须落在登记时绑定的浏览器会话中。

## 测试

```sh
npm test
```

覆盖已知答案向量、伪造 `interact_ref`（INVALID_HASH）、跨会话回调（CROSS_SESSION_CALLBACK）、重复/重放回调（DUPLICATE_CALLBACK）以及未知事务与空输入。
