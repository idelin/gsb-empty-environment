# Cross-device confirmation protocol

这是一个可导入的 TypeScript 认证宿主实现。它只消费抽象协议事件（登录发起、邮件已投递、链接被打开/被扫描器预取、登记设备确认、浏览器证明、签发结果），**不发送邮件**，也不依赖具体邮件或浏览器供应商。

核心特性：

- 邮件令牌由宿主签名，绑定账户、邮件地址、发起浏览器、浏览器窗口、浏览器公钥、挑战、用途和截止时间。
- 打开邮件或安全扫描器预取只写审计事件，不消耗授权、不改变挑战状态。
- 登记设备在确认页明确选择某个发起浏览器后签名确认；只有该浏览器的私钥持有者能继续领取。
- 多封邮件、同一用户码的多个窗口、跨账户同邮件地址均以 `tokenId` / `challengeId` 隔离。
- 浏览器和登记设备标识按账户分区；两个账户可以使用相同的 `browserId` / `deviceId` 而不会互相串用授权。
- 支持登记设备替换/撤销和浏览器密钥换代。
- 确认消息可乱序到达；旧确认不会匹配到新邮件。
- 签发使用确定性事务号。签发意图先持久化；一旦存在意图，后续领取只查询同一事务，绝不再次签名。
- 签发成功但回执丢失时返回 `unknown`，恢复后通过 `lookup` 收敛。
- 提供投递前/投递后不可变签名回执和完全离线验收。
- 追加式、哈希链式日志；在每个持久化前缀上都能重放并回答哪些主体拥有有效授权。

## Quick start

```ts
import {
  createAuthHost,
  createBrowserCredentialDouble,
  createDeviceCredentialDouble,
  createDeviceConfirmation,
  createBrowserProof,
  InMemoryIssuanceService,
  MemoryJournalStore,
  verifyIssuedAuthorization,
} from "./src/index.ts";

const issuance = new InMemoryIssuanceService();
const host = await createAuthHost({
  store: new MemoryJournalStore(),
  issuance,
});

const browser = createBrowserCredentialDouble();
const device = createDeviceCredentialDouble();

await host.registerBrowser({
  accountId: "account-a",
  browserId: "browser-a",
  publicKey: browser.publicKey,
});
await host.registerDevice({
  accountId: "account-a",
  deviceId: "device-a",
  publicKey: device.publicKey,
});

const login = await host.beginLogin({
  accountId: "account-a",
  email: "person@example.test",
  browserId: "browser-a",
  browserSessionId: "window-1",
});

// The host does not send email. The caller sends login.emailToken.
await host.recordEmailOpened({
  token: login.emailToken,
  kind: "scanner",          // scan/prefetch never consumes the token
  accessId: "scanner-1",
});
await host.recordEmailDelivered({
  token: login.emailToken,
  deliveryId: "delivery-1",
});

// On the registered device, the user selects the initiating browser.
const confirmation = createDeviceConfirmation(device, login.emailToken, {
  deviceId: "device-a",
  browserId: "browser-a",
  browserSessionId: "window-1",
});
await host.confirmOnDevice(confirmation);

// Only the selected browser, holding the token-bound private key, can claim.
const proof = createBrowserProof(browser, login.emailToken);
const redeemed = await host.redeem({ token: login.emailToken, proof });

if (redeemed.state === "issued") {
  const offline = verifyIssuedAuthorization(redeemed.receipt!, host.publicKey);
  console.log(offline.accepted, offline.transactionId);
}
```

## Token binding

`EmailTokenClaims` commits to:

| Field | Meaning |
| --- | --- |
| `accountId` | account owning the login |
| `email` | delivery address; never used as an identity key |
| `browserId` | initiating browser installation |
| `browserSessionId` | optional window/session discriminator |
| `browserKeyId` / `browserPublicKey` | exact key generation for the challenge |
| `challengeId` / `challenge` | unique challenge, not a shared user code |
| `purpose` | authorization purpose |
| `expiresAt` | absolute deadline |
| `tokenId` / `userCode` | opaque lookup id / display code |

The `userCode` is intentionally stable for the same account, email and purpose, so two windows can display the same code. It is **not** a lookup or matching key. The confirmation page must select a concrete `tokenId` or `challengeId`.

## State machine

`host.getStatus(...)` (also `getChallengeStatus`) returns `state` and the alias `status`:

- `awaiting_device_confirmation`: no valid registered-device approval yet.
- `awaiting_browser_proof`: approval exists, waiting for the bound browser key.
- `issued`: the external signer produced a credential and the host persisted its receipt.
- `expired`: the challenge deadline passed before issuance.
- `revoked`: the initiating browser was revoked before issuance.
- `unknown`: an issuance intent exists but no conclusive terminal result is known (lost response, pending lookup, or `not_found` after an intent). A subsequent claim only performs `lookup(transactionId)`.
- `failed`: the signer reported a terminal failure.

`unknown` also covers a token/challenge that cannot be resolved, with a `reason` such as `unknown_token`; this prevents accidentally attaching an out-of-order confirmation to a newer email.

Opening and prefetch are audited separately (`emailOpenCount`, `scannerAccessCount`) and cannot move the state machine.
Status/list responses never include the issued credential or its receipt; they expose `receiptHash` only. The credential is returned by `redeem` after a valid browser proof, even when the state is already `issued`.

## Revocation and key rotation

- `revokeBrowser({ accountId, browserId })` immediately prevents new confirmations/issuance. An issuance already committed after a durable intent remains visible as `issued`, and the audit marks it `browserRevoked`.
- `rotateBrowserKey({ accountId, browserId, publicKey })` retires the previous browser key. A challenge bound to the retired key cannot be completed; start a new login with the new key. A proof signed by the new key is rejected as a binding mismatch.
- `replaceDevice(...)` atomically revokes the old approver and registers the new one. A still-pending confirmation from the old device becomes invalid and the new device can approve it.
- `revokeDevice(...)` has the same invalidating effect on pending confirmations.
- Already-issued receipts are immutable evidence, even if a later revocation makes the authorization no longer currently valid.

## Issuance idempotency and crash windows

The transaction id is derived from the token/challenge/browser/key/purpose binding:

```ts
transactionId = deriveTransactionId(token.claims)
```

Redemption follows these durable steps:

1. Persist `browser.proof_accepted`.
2. Persist `issuance.intent` with the exact deterministic request.
3. Call the external signer **once**.
4. Persist `issuance.result` (`issued`, `unknown`, or `failed`).

If the process stops after step 2 or after the external signer commits but before step 4, recovery sees an intent without a terminal result. Any later claim calls only:

```ts
issuance.lookup(transactionId)
```

It never calls `issue` again. A `pending` or `not_found` lookup keeps the state `unknown` rather than risking a second signature.

The supplied `InMemoryIssuanceService` models this explicitly: it records the committed credential before throwing a simulated lost response.

## Receipts and offline acceptance

Every receipt is signed by the host Ed25519 key and deeply frozen:

- `email.dispatch`: created when the email token is issued, before delivery.
- `email.delivery`: references the dispatch receipt hash and token hash.
- `device.confirmation`: commits to the selected browser, device key and confirmation signature.
- `browser.revoked` / `device.replaced`: revocation evidence.
- `authorization.issued`: embeds the signed token, device confirmation, browser proof, credential and all binding hashes.

Offline verification needs only the host public key:

```ts
verifyEmailDeliveryPair(dispatchReceipt, deliveryReceipt, host.publicKey);
verifyIssuedAuthorization(issuedReceipt, host.publicKey);
verifyReceipt(receipt, host.publicKey);
verifyReceiptChain(receipts, host.publicKey);
```

`verifyIssuedAuthorization` rechecks the token signature, challenge/browser/key/purpose/expiry binding, device signature, browser signature, transaction id, credential hash and receipt signature without contacting the host.

## Durability and authorization audit

`JournalStore` is append-only. The built-in implementations are:

- `MemoryJournalStore` for tests and ephemeral hosts.
- `FileJournalStore` with one checksummed-by-hash JSON record per line, `fsync` after each append, and torn-tail truncation during recovery.
- `FaultInjectingJournalStore` for append-before/append-after crash tests.

Every event is hash-chained and signed. On startup the host verifies the complete prefix and replays it into state. Because each logical transition is a single event (or the intentional `intent` / `result` pair for issuance), every durable prefix is a valid state.

`host.authorizationAudit()` returns:

- active/revoked browsers and their key generations,
- active/replaced/revoked devices,
- issued authorizations with `valid` (not revoked, key not retired, credential not expired),
- pending challenge states,
- `authorizedPrincipals` for the browsers that currently hold a valid issued authorization.

For durable storage, supply a stable `hostKeyPair`; otherwise a newly started host generates a new key and cannot verify the old journal or receipts.

## Tests

```sh
npm test
```

The suite covers:

- scanner/prefetch followed by a real user confirmation,
- two windows with the same user code,
- the same email address under two accounts,
- out-of-order confirmations and expired challenges,
- device switching/replacement and browser key rotation,
- revocation before intent and revocation interleaved with in-flight issuance,
- lost issuance receipt and same-transaction lookup,
- restart after a lost receipt with no second signature,
- every journal prefix after an interrupted persist action,
- torn file-journal tail recovery,
- offline receipt and delivery verification.
