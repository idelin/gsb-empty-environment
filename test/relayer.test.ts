import assert from "node:assert/strict";
import { test } from "node:test";
import { SignatureVerificationError, TypedDataRelayer, VerificationErrorCode } from "../src/index.js";
import { InMemoryNonceStore } from "../src/index.js";
import { keccak256 } from "./support/keccak.js";
import {
  recoverAddress,
  signDigest,
  signatureToBytes,
  toHighS,
} from "./support/secp256k1.js";
import {
  AUTHORIZATION_TYPES,
  buildDomain,
  buildMessage,
  CHAIN_ID,
  digestFor,
  FUTURE_DEADLINE,
  makeSigner,
  NOW,
  OTHER_CHAIN_ID,
  OTHER_CONTRACT,
  PAST_DEADLINE,
  signAuthorization,
  TARGET_CONTRACT,
  type AuthorizationMessage,
  type SignerIdentity,
} from "./fixtures.js";

function createRelayer(nowValue: bigint = NOW) {
  const store = new InMemoryNonceStore();
  const relayer = new TypedDataRelayer({
    chainId: CHAIN_ID,
    targetContract: TARGET_CONTRACT,
    keccak256,
    recoverAddress,
    nonceStore: store,
    now: () => nowValue,
  });
  return { store, relayer };
}

function submit(
  relayer: TypedDataRelayer,
  signer: SignerIdentity,
  domain = buildDomain(),
  messageOverrides: Partial<AuthorizationMessage> = {},
) {
  const message = buildMessage(signer, messageOverrides);
  const signature = signAuthorization(signer, domain, message);
  return relayer.relay({
    domain,
    types: AUTHORIZATION_TYPES,
    primaryType: "Authorization",
    message,
    signature,
  });
}

test("合法请求通过并原子递增 nonce，可通过 onAccepted 执行后续动作", async () => {
  const { relayer } = createRelayer();
  const signer = makeSigner(0x1234567890abcdefn);
  const accepted: string[] = [];

  const first = await submit(relayer, signer, undefined, undefined);
  assert.equal(first.signer, signer.address);
  assert.equal(first.nonce, 0n);

  const result = await submit(relayer, signer, undefined, { nonce: 1n });
  assert.equal(result.nonce, 1n);
  assert.equal(await relayer.getNonce(signer.address), 2n);

  await submit(relayer, signer, undefined, {
    nonce: 2n,
    deadline: FUTURE_DEADLINE,
  });
  assert.ok(true);

  // onAccepted 在 nonce 递增之后被调用。
  let acceptedNonce: bigint | null = null;
  await submit(relayer, signer, undefined, { nonce: 3n });
  await relayer.relay(
    relayRequest(signer, { nonce: 4n }),
    (_req, relayResult) => {
      acceptedNonce = relayResult.nonce;
      accepted.push("broadcast");
    },
  );
  assert.equal(acceptedNonce, 4n);
  assert.deepEqual(accepted, ["broadcast"]);
});

test("换链重放：chainId 不同直接拒绝，且不消费 nonce", async () => {
  const { relayer, store } = createRelayer();
  const signer = makeSigner(0x1111n);
  const foreignDomain = buildDomain({ chainId: OTHER_CHAIN_ID });
  const message = buildMessage(signer);
  const signature = signAuthorization(signer, foreignDomain, message);

  await assert.rejects(
    relayer.relay({
      domain: foreignDomain,
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message,
      signature,
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.CHAIN_ID_MISMATCH,
  );
  assert.equal(await store.get(signer.address), 0n);
});

test("域中缺失 chainId 或 verifyingContract 一律拒绝", async () => {
  const { relayer } = createRelayer();
  const signer = makeSigner(0x2222n);

  await assert.rejects(
    submit(relayer, signer, buildDomain({ chainId: undefined })),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.MISSING_CHAIN_ID,
  );

  await assert.rejects(
    submit(relayer, signer, buildDomain({ verifyingContract: undefined })),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.MISSING_VERIFYING_CONTRACT,
  );
});

test("换合约重放：verifyingContract 不等于目标合约时拒绝", async () => {
  const { relayer, store } = createRelayer();
  const signer = makeSigner(0x3333n);
  const foreignDomain = buildDomain({ verifyingContract: OTHER_CONTRACT });
  const message = buildMessage(signer);
  const signature = signAuthorization(signer, foreignDomain, message);

  await assert.rejects(
    relayer.relay({
      domain: foreignDomain,
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message,
      signature,
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.VERIFYING_CONTRACT_MISMATCH,
  );
  assert.equal(await store.get(signer.address), 0n);
});

test("nonce 复用：同一 nonce 第二次提交被拒绝，链上值已前进", async () => {
  const { relayer } = createRelayer();
  const signer = makeSigner(0x4444n);

  await submit(relayer, signer, undefined, { nonce: 0n });

  await assert.rejects(
    submit(relayer, signer, undefined, { nonce: 0n }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.NONCE_MISMATCH,
  );
  assert.equal(await relayer.getNonce(signer.address), 1n);
});

test("跳过的 nonce 与缺失 nonce 字段被拒绝", async () => {
  const { relayer } = createRelayer();
  const signer = makeSigner(0x5555n);

  await assert.rejects(
    submit(relayer, signer, undefined, { nonce: 9n }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.NONCE_MISMATCH,
  );

  const message = buildMessage(signer);
  const withoutNonce = { ...message };
  delete (withoutNonce as Partial<AuthorizationMessage>).nonce;
  const signature = signAuthorization(signer, buildDomain(), message);
  await assert.rejects(
    relayer.relay({
      domain: buildDomain(),
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message: withoutNonce,
      signature,
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.MISSING_NONCE,
  );
});

test("过期 deadline 拒绝，恰好在当前时间的 deadline 允许", async () => {
  const relayerNow = NOW;
  const { relayer } = createRelayer(relayerNow);
  const signer = makeSigner(0x6666n);

  await assert.rejects(
    submit(relayer, signer, undefined, { deadline: PAST_DEADLINE }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.DEADLINE_EXPIRED,
  );

  const result = await submit(relayer, signer, undefined, {
    deadline: relayerNow,
  });
  assert.equal(result.nonce, 0n);
});

test("缺失 deadline 字段拒绝", async () => {
  const { relayer } = createRelayer();
  const signer = makeSigner(0x7777n);
  const message = buildMessage(signer);
  const withoutDeadline = { ...message };
  delete (withoutDeadline as Partial<AuthorizationMessage>).deadline;
  const signature = signAuthorization(signer, buildDomain(), message);

  await assert.rejects(
    relayer.relay({
      domain: buildDomain(),
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message: withoutDeadline,
      signature,
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.MISSING_DEADLINE,
  );
});

test("高 s 值签名在恢复之前就被拒绝", async () => {
  const { relayer, store } = createRelayer();
  const signer = makeSigner(0x8888n);
  const domain = buildDomain();
  const message = buildMessage(signer);
  const digest = digestFor(domain, message);
  const lowS = signDigest(digest, signer.privateKey);
  const highS = toHighS(lowS);

  await assert.rejects(
    relayer.relay({
      domain,
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message,
      signature: signatureToBytes(highS),
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.HIGH_S,
  );
  assert.equal(await store.get(signer.address), 0n);

  // 同一私钥的低 s 原始签名仍可正常通过。
  const result = await relayer.relay({
    domain,
    types: AUTHORIZATION_TYPES,
    primaryType: "Authorization",
    message,
    signature: signatureToBytes(lowS),
  });
  assert.equal(result.nonce, 0n);
});

test("v 不是 27/28、长度错误与非法编码被拒绝", async () => {
  const { relayer } = createRelayer();
  const signer = makeSigner(0x9999n);
  const domain = buildDomain();
  const message = buildMessage(signer);
  const digest = digestFor(domain, message);
  const valid = signatureToBytes(signDigest(digest, signer.privateKey));

  const badV = new Uint8Array(valid);
  badV[64] = 0;
  await assert.rejects(
    relayer.relay({
      domain,
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message,
      signature: badV,
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.INVALID_V,
  );

  await assert.rejects(
    relayer.relay({
      domain,
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message,
      signature: valid.slice(0, 64),
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.INVALID_SIGNATURE_LENGTH,
  );

  await assert.rejects(
    relayer.relay({
      domain,
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message,
      signature: "0x1234",
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.INVALID_SIGNATURE_LENGTH,
  );
});

test("恢复地址与消息声明签名者不一致时拒绝", async () => {
  const { relayer, store } = createRelayer();
  const actualSigner = makeSigner(0xaaa1n);
  const impostor = makeSigner(0xaaa2n);

  // 用 actualSigner 签名，消息却声明 impostor 为签名者。
  await assert.rejects(
    submit(relayer, actualSigner, undefined, { signer: impostor.address }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.SIGNER_MISMATCH,
  );
  assert.equal(await store.get(impostor.address), 0n);
  assert.equal(await store.get(actualSigner.address), 0n);
});

test("被篡改的消息内容导致恢复地址不匹配而拒绝", async () => {
  const { relayer } = createRelayer();
  const signer = makeSigner(0xbbbbn);
  const domain = buildDomain();
  const original = buildMessage(signer, { value: 1_000n });
  const signature = signAuthorization(signer, domain, original);
  const tampered = buildMessage(signer, { value: 999_999n });

  await assert.rejects(
    relayer.relay({
      domain,
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message: tampered,
      signature,
    }),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.SIGNER_MISMATCH,
  );
});

test("不同签名者维护独立 nonce", async () => {
  const { relayer } = createRelayer();
  const alice = makeSigner(0xc1n);
  const bob = makeSigner(0xc2n);

  await submit(relayer, alice, undefined, { nonce: 0n });
  await submit(relayer, alice, undefined, { nonce: 1n });
  const bobResult = await submit(relayer, bob, undefined, { nonce: 0n });

  assert.equal(await relayer.getNonce(alice.address), 2n);
  assert.equal(bobResult.nonce, 0n);
  assert.equal(await relayer.getNonce(bob.address), 1n);
});

// 测试辅助：构造一次完整 relay 请求。
function relayRequest(
  signer: SignerIdentity,
  overrides: Partial<AuthorizationMessage> = {},
) {
  const domain = buildDomain();
  const message = buildMessage(signer, overrides);
  return {
    domain,
    types: AUTHORIZATION_TYPES,
    primaryType: "Authorization",
    message,
    signature: signAuthorization(signer, domain, message),
  };
}
