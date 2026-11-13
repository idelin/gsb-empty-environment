# sealed-sender-validator

密封发送者（Sealed Sender）消息的 TypeScript 校验包，零运行时依赖，同时覆盖服务端的
Unidentified-Access-Key 校验与客户端侧的发送者证书校验。可直接以 ESM 导入，
也可用 `tsc` 编译为带 `.d.ts` 的 JS 包。

## 安全目标

- 防止匿名骚扰：没有为收件人登记的有效访问密钥，密封消息一律拒绝。
- 防止伪造发送者：发送者证书必须链至受信根，且身份密钥、发送者标识与消息绑定一致。
- 防止时序侧信道：密钥材料全部走常数时间比较；收件人缺失时执行随机假密钥比较，
  所有失败路径返回同一个错误，杜绝收件人枚举与密钥探测。
- 过期静默丢弃：证书超出有效期（含未生效）的消息仅丢弃，不发送任何回执。

## 运行环境

- Node.js >= 22.7（直接用类型擦除运行 `.ts`）或任意现代浏览器；
- 全局 `crypto.subtle`（Node 19+ / 浏览器默认提供）；
- 无第三方依赖。

## 快速开始

### 1. 服务端校验 Unidentified-Access-Key

```ts
import {
  verifyUnidentifiedAccessKey,
  AccessKeyError,
  type AccessKeyRegistry,
} from "sealed-sender-validator";

// 接入方实现：按收件人查询其登记的 16 字节访问密钥
const registry: AccessKeyRegistry = async (recipientId) => {
  return store.findAccessKey(recipientId);
};

try {
  const record = await verifyUnidentifiedAccessKey({
    presentedKey: request.headers["unidentified-access-key"], // Uint8Array / hex / base64
    recipientId: envelope.recipientId,
    registry,
  });
} catch (error) {
  if (error instanceof AccessKeyError) {
    // 收件人不存在、未登记、密钥畸形、密钥不匹配：全部回同一个 401
    return response.status(401).json({ error: "unidentified access key rejected" });
  }
  throw error;
}
```

### 2. 客户端校验发送者证书

```ts
import {
  verifySenderCertificate,
  CERTIFICATE_CLOCK_SKEW_MS,
  CertificateExpiredError,
} from "sealed-sender-validator";

try {
  await verifySenderCertificate({
    certificate: message.senderCertificate,
    sessionIdentityKey: decrypted.sessionIdentityKey,
    declaredSender: message.declaredSender,
    trustedRoots: [{ rootId: "root-2026", publicKey: trustedRootPublicKey }],
    // now 可选，默认 Date.now()；容差默认 5 分钟
    clockSkewMs: CERTIFICATE_CLOCK_SKEW_MS,
  });
} catch (error) {
  if (error instanceof CertificateExpiredError) {
    return; // 静默丢弃：不投递、不发投递回执/已读回执/错误回执
  }
  throw error; // 其余伪造/绑定问题按常规拒收处理
}
```

### 3. 一条流水线处理整条密封消息

```ts
import { processSealedSender, isSilentDrop } from "sealed-sender-validator";

const result = await processSealedSender({
  message: sealedMessage,
  registry,
  trustedRoots,
});

if (!result.accepted) {
  if (isSilentDrop(result)) {
    return; // 证书过期：仅丢弃，不发任何回执
  }
  return response.status(401).json({ error: result.reason });
}

await deliver(result.recipientId, sealedMessage);
await sendDeliveryReceipt(result.sender); // 仅 accepted 才允许发回执
```

## 校验规则

### Unidentified-Access-Key（服务端，`src/access-key.ts`）

- 提交值接受 `Uint8Array`、hex 或 base64，归一化为固定 16 字节；
- 与收件人登记值用 `timingSafeEqualBytes` 做常数时间异或累积比较；
- 收件人不存在、未登记、输入畸形、长度不符、比较不匹配，全部抛出同一个
  `AccessKeyError`（code 固定为 `ACCESS_KEY_REJECTED`，文案固定）；
- 收件人缺失时生成随机假密钥并照常执行一次比较，抹平分支耗时。

### 发送者证书（客户端，`src/certificate.ts`）

按顺序执行，任一失败即终止：

1. 结构完整（发送者、根标识、身份密钥、签名负载、签名、时间字段）；
2. 签名由 `rootId` 对应的受信根验签通过（`CERT_UNTRUSTED_ROOT`）；
3. 当前时间落在 `[validFrom - 5m, validUntil + 5m]` 内，边界含容差；
   超出区间（过期或未生效）抛 `CertificateExpiredError`（code `CERT_EXPIRED`）；
4. 证书 `identityKey` 与会话身份密钥常数时间相等（`CERT_IDENTITY_MISMATCH`）；
5. 证书 `sender` 与消息声明发送者常数时间相等（`CERT_SENDER_MISMATCH`），
   可选传入 `normalizeSender` 对两边做相同归一化。

签名算法通过 `SignatureVerifier` 接口注入，默认提供 ECDSA P-256 + SHA-256
（Web Crypto）。对接 Ed25519 等体系时实现该接口并挂到 `TrustedRoot.verifier` 即可，
证书的 `signedData` 为接入方自己的规范化编码字节，本包不绑定序列化格式。

### 回执策略（`src/sealed-sender.ts`）

- `accepted: true`：允许投递，`deliveryReceiptAllowed: true`；
- `reason === "CERT_EXPIRED"`：`silent: true`，仅丢弃，不发任何回执；
- 其余拒绝：`silent: false`，按常规拒收响应处理（如统一 401）。

## 导出的 API

- `verifyUnidentifiedAccessKey` / `normalizeAccessKey` / `ACCESS_KEY_LENGTH`
- `verifySenderCertificate` / `CERTIFICATE_CLOCK_SKEW_MS`
- `processSealedSender` / `isSilentDrop`
- `timingSafeEqualBytes` / `timingSafeEqualString`
- `AccessKeyError` / `CertificateError` / `CertificateExpiredError` /
  `isCertificateExpiredError`
- `EcdsaP256Sha256Verifier` / `defaultSignatureVerifier` / 接口 `SignatureVerifier`

## 测试与构建

```bash
npm test        # node:test，35 个用例，零依赖
npm run build   # tsc 输出 dist/（含 .d.ts）
```

测试覆盖：常数时间比较、统一错误与收件人枚举防护、受信根验签、
5 分钟容差边界（含未生效）、身份/发送者绑定、过期静默丢弃与回执抑制。
