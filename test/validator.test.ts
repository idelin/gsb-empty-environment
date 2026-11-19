import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  FUTURE_LEEWAY_SECONDS,
  HubValidator,
  MessageType,
  ValidationErrorCode,
  bytesToHex,
} from "../index.ts";
import type {
  Message,
  MessageData,
  ValidationDeps,
} from "../index.ts";

const NOW = 1_800_000_000;
const FID = 8642;

const CUSTODY = new TextEncoder().encode("custody-of-8642");
const SIGNER_A = new TextEncoder().encode("signer-A");
const SIGNER_B = new TextEncoder().encode("signer-B");

/** 测试用确定性“编解码器”：JSON 序列化，Uint8Array 用 {$bytes: hex} 表示。 */
function encodeData(data: MessageData): Uint8Array {
  const json = JSON.stringify(data, (_key, value) =>
    value instanceof Uint8Array ? { $bytes: bytesToHex(value) } : value,
  );
  return new TextEncoder().encode(json);
}

function decodeData(dataBytes: Uint8Array): MessageData {
  return JSON.parse(new TextDecoder().decode(dataBytes), (_key, value) => {
    if (
      value !== null &&
      typeof value === "object" &&
      typeof value.$bytes === "string"
    ) {
      const hex = value.$bytes as string;
      const bytes = new Uint8Array(hex.length / 2);
      for (let i = 0; i < bytes.length; i++) {
        bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
      }
      return bytes;
    }
    return value;
  }) as MessageData;
}

function makeDeps(): ValidationDeps {
  return {
    hashData: (dataBytes) => createHash("sha256").update(dataBytes).digest(),
    decodeData,
    // 测试用“验签”：签名必须是 utf8("sig:" + hex(signer))。
    verifySignature: (message) =>
      bytesToHex(message.signature) ===
      bytesToHex(
        new TextEncoder().encode(`sig:${bytesToHex(message.signer)}`),
      ),
    isCustodySigner: (fid, signer) =>
      fid === FID && bytesToHex(signer) === bytesToHex(CUSTODY),
    now: () => NOW,
  };
}

function makeMessage(
  data: MessageData,
  signer: Uint8Array,
  overrides: Partial<Pick<Message, "hash" | "signature" | "dataBytes">> = {},
): Message {
  const dataBytes = overrides.dataBytes ?? encodeData(data);
  return {
    data,
    dataBytes,
    hash:
      overrides.hash ?? createHash("sha256").update(dataBytes).digest(),
    signature:
      overrides.signature ??
      new TextEncoder().encode(`sig:${bytesToHex(signer)}`),
    signer,
  };
}

function castData(text: string, timestamp: number): MessageData {
  return {
    fid: FID,
    type: MessageType.CAST_ADD,
    timestamp,
    body: { castAdd: { text } },
  };
}

function signerAddData(target: Uint8Array, timestamp: number): MessageData {
  return {
    fid: FID,
    type: MessageType.SIGNER_ADD,
    timestamp,
    body: { signerAdd: { signer: target } },
  };
}

function signerRemoveData(target: Uint8Array, timestamp: number): MessageData {
  return {
    fid: FID,
    type: MessageType.SIGNER_REMOVE,
    timestamp,
    body: { signerRemove: { signer: target } },
  };
}

function newValidator(): HubValidator {
  return new HubValidator(makeDeps());
}

function addSigner(
  validator: HubValidator,
  target: Uint8Array,
  timestamp: number,
  by: Uint8Array = CUSTODY,
): void {
  const result = validator.validateAndStore(
    makeMessage(signerAddData(target, timestamp), by),
  );
  assert.deepEqual(result, { ok: true });
}

test("接受合法消息：custody 添加签名者后，签名者可发 cast", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  const result = v.validateAndStore(
    makeMessage(castData("hello farcaster", NOW - 50), SIGNER_A),
  );
  assert.deepEqual(result, { ok: true });
  assert.equal(v.getMessages(FID).length, 2);
});

test("拒绝类型与正文不匹配：CAST_ADD 类型携带 signerAdd 正文", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  const mismatched: MessageData = {
    fid: FID,
    type: MessageType.CAST_ADD,
    timestamp: NOW - 50,
    body: { signerAdd: { signer: SIGNER_B } },
  };
  const result = v.validateAndStore(makeMessage(mismatched, SIGNER_A));
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, ValidationErrorCode.TYPE_BODY_MISMATCH);
  }
});

test("拒绝类型与正文不匹配：正文同时设置两个字段", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  const mismatched: MessageData = {
    fid: FID,
    type: MessageType.CAST_ADD,
    timestamp: NOW - 50,
    body: {
      castAdd: { text: "hi" },
      castRemove: { targetHash: new Uint8Array([1, 2, 3]) },
    },
  };
  const result = v.validateAndStore(makeMessage(mismatched, SIGNER_A));
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, ValidationErrorCode.TYPE_BODY_MISMATCH);
  }
});

test("拒绝 hash 不等于 hash(data_bytes) 的消息", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  const badHash = new Uint8Array(32).fill(0xaa);
  const result = v.validateAndStore(
    makeMessage(castData("tampered hash", NOW - 50), SIGNER_A, {
      hash: badHash,
    }),
  );
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.code, ValidationErrorCode.INVALID_HASH);
});

test("拒绝 data_bytes 重新解码后与结构化字段不一致的消息", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  // data_bytes 编码的是 "on-chain text"，结构化字段却声称 "forged text"。
  const onChain = castData("on-chain text", NOW - 50);
  const forged = castData("forged text", NOW - 50);
  const result = v.validateAndStore(
    makeMessage(forged, SIGNER_A, { dataBytes: encodeData(onChain) }),
  );
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, ValidationErrorCode.DATA_BYTES_MISMATCH);
  }
});

test("拒绝时间戳晚于当前时间 10 分钟以上的消息，恰好 10 分钟可接受", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  const tooFar = v.validateAndStore(
    makeMessage(
      castData("from the future", NOW + FUTURE_LEEWAY_SECONDS + 1),
      SIGNER_A,
    ),
  );
  assert.equal(tooFar.ok, false);
  if (!tooFar.ok) {
    assert.equal(
      tooFar.code,
      ValidationErrorCode.TIMESTAMP_TOO_FAR_FUTURE,
    );
  }
  const atLimit = v.validateAndStore(
    makeMessage(castData("at the limit", NOW + FUTURE_LEEWAY_SECONDS), SIGNER_A),
  );
  assert.deepEqual(atLimit, { ok: true });
});

test("拒绝无效签名", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  const result = v.validateAndStore(
    makeMessage(castData("bad sig", NOW - 50), SIGNER_A, {
      signature: new TextEncoder().encode("sig:forged"),
    }),
  );
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, ValidationErrorCode.INVALID_SIGNATURE);
  }
});

test("拒绝不在活跃签名者集合中的签名者", () => {
  const v = newValidator();
  const result = v.validateAndStore(
    makeMessage(castData("no signer", NOW - 50), SIGNER_A),
  );
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, ValidationErrorCode.INACTIVE_SIGNER);
  }
  // 非 custody、非活跃签名者也不能签发 SignerAdd 自我授权。
  const selfAdd = v.validateAndStore(
    makeMessage(signerAddData(SIGNER_A, NOW - 40), SIGNER_A),
  );
  assert.equal(selfAdd.ok, false);
  if (!selfAdd.ok) {
    assert.equal(selfAdd.code, ValidationErrorCode.INACTIVE_SIGNER);
  }
});

test("SignerRemove 逐出该签名者此前全部消息，之后它的新消息被拒绝", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  addSigner(v, SIGNER_B, NOW - 100);
  assert.deepEqual(
    v.validateAndStore(makeMessage(castData("cast-1", NOW - 90), SIGNER_A)),
    { ok: true },
  );
  assert.deepEqual(
    v.validateAndStore(makeMessage(castData("cast-2", NOW - 80), SIGNER_A)),
    { ok: true },
  );
  assert.deepEqual(
    v.validateAndStore(makeMessage(castData("b-cast", NOW - 70), SIGNER_B)),
    { ok: true },
  );
  assert.equal(v.getMessages(FID).length, 5);

  // custody 移除 SIGNER_A。
  const remove = v.validateAndStore(
    makeMessage(signerRemoveData(SIGNER_A, NOW - 60), CUSTODY),
  );
  assert.deepEqual(remove, { ok: true });

  // SIGNER_A 的两条 cast 被逐出；SIGNER_B 的消息与 custody 签发的 SignerAdd 保留。
  const remaining = v.getMessages(FID);
  assert.equal(
    remaining.every((m) => bytesToHex(m.signer) !== bytesToHex(SIGNER_A)),
    true,
  );
  assert.equal(
    remaining.some((m) => bytesToHex(m.signer) === bytesToHex(SIGNER_B)),
    true,
  );
  assert.equal(v.getEvictedMessages(FID).length, 2);
  assert.equal(
    v
      .getEvictedMessages(FID)
      .every((m) => bytesToHex(m.signer) === bytesToHex(SIGNER_A)),
    true,
  );
  assert.equal(v.isActiveSigner(FID, SIGNER_A), false);

  // 移除后重放：SIGNER_A 的新消息被拒绝。
  const replay = v.validateAndStore(
    makeMessage(castData("replay after removal", NOW - 10), SIGNER_A),
  );
  assert.equal(replay.ok, false);
  if (!replay.ok) {
    assert.equal(replay.code, ValidationErrorCode.INACTIVE_SIGNER);
  }
});

test("SignerAdd 与 SignerRemove 时间戳相同时移除胜出", () => {
  const v = newValidator();
  const T = NOW - 100;
  addSigner(v, SIGNER_A, T);
  // 同一时间戳的移除：平局，移除胜出。
  const remove = v.validateAndStore(
    makeMessage(signerRemoveData(SIGNER_A, T), CUSTODY),
  );
  assert.deepEqual(remove, { ok: true });
  assert.equal(v.isActiveSigner(FID, SIGNER_A), false);
  const cast = v.validateAndStore(
    makeMessage(castData("tie cast", NOW - 50), SIGNER_A),
  );
  assert.equal(cast.ok, false);
  if (!cast.ok) assert.equal(cast.code, ValidationErrorCode.INACTIVE_SIGNER);

  // 更晚时间戳的重新添加可以盖过移除。
  addSigner(v, SIGNER_A, T + 10);
  assert.equal(v.isActiveSigner(FID, SIGNER_A), true);
  assert.deepEqual(
    v.validateAndStore(makeMessage(castData("back again", NOW - 40), SIGNER_A)),
    { ok: true },
  );
});

test("签名者可签发 SignerRemove 移除自己", () => {
  const v = newValidator();
  addSigner(v, SIGNER_A, NOW - 100);
  const selfRemove = v.validateAndStore(
    makeMessage(signerRemoveData(SIGNER_A, NOW - 50), SIGNER_A),
  );
  assert.deepEqual(selfRemove, { ok: true });
  assert.equal(v.isActiveSigner(FID, SIGNER_A), false);
  const after = v.validateAndStore(
    makeMessage(castData("too late", NOW - 40), SIGNER_A),
  );
  assert.equal(after.ok, false);
  if (!after.ok) assert.equal(after.code, ValidationErrorCode.INACTIVE_SIGNER);
});
