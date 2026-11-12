# SRP 服务端公钥参数拒绝

SRP-6a 服务端若接受 `A` 为 `0` 或 `N` 的倍数，攻击者无需口令就能让会话密钥变成固定值。本包使用 RFC 5054 的 3072 位群，并在协议入口拒绝 `A mod N === 0` 与 `u === 0`。

默认哈希为 SHA-256；如需与原 RFC 5054 线格式互通，可显式传入 `{ hash: "sha1" }`。

## 使用

```ts
import {
  SrpServer,
  deriveVerifier,
  generateSalt,
} from "./srp.ts";

const salt = generateSalt(16);
const record = {
  username: "alice",
  salt,
  verifier: deriveVerifier("alice", password, salt),
};

// b 在构造时由 CSPRNG 生成，B 非零。
const server = new SrpServer(record);

// 收到客户端的 A 后建立挑战；A=0、A=N、A=2N 等会抛出 SrpError。
const challenge = server.challenge(clientA);

// 以下值随 ServerKeyExchange 发给客户端。
challenge.salt;
challenge.Bbytes; // 384 字节、已按 N 长度填充

// 客户端返回 M1 后验证；比较使用 crypto.timingSafeEqual。
const proof = server.verifyClientProof(clientM1);
if (proof === null) {
  // 认证失败，不要返回 M2。
} else {
  // proof.M2 发回客户端，proof.K 可用于后续受保护会话。
}
```

`A` 必须满足 `0 < A < N`。`SrpServer` 还会验证 `u !== 0`、验证器范围、非零 `B`，并且 `M1` 绑定用户名、盐、`A`、`B` 和会话密钥 `K`，`M2 = H(A | M1 | K)`。

## 验证

```sh
npm test
```

测试覆盖 `A=0`、`A=N`、`A=2N`、零字节编码、错误口令及固定合法握手向量。
