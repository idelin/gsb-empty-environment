import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  GnapInteractionVerifier,
  GnapVerificationError,
  calculateInteractionHash,
  constantTimeHashEquals,
} from "../src/gnap-interaction.ts";

const CLIENT_NONCE = "client-nonce-123";
const AS_NONCE = "as-nonce-456";
const REAL_INTERACT_REF = "interact-ref-abc";
const ATTACKER_INTERACT_REF = "attacker-owned-ref-xyz";
const AUTHORIZATION_URL = "https://as.example.com/gnap/tx";
const VICTIM_SESSION = "browser-session-victim";
const ATTACKER_SESSION = "browser-session-attacker";
const TX_ID = "tx-0001";

// 与 RFC 9724 计算方式一致的已知答案向量，
// 已用独立的 node:crypto 调用预先生成。
const KNOWN_ANSWER = "yadKn_TczxdqAsMIQdCxIR9PxNYBrZ_QGFpf6HSE2s4";

function registerVictimTransaction(): GnapInteractionVerifier {
  const verifier = new GnapInteractionVerifier();
  verifier.register(TX_ID, {
    clientNonce: CLIENT_NONCE,
    asNonce: AS_NONCE,
    browserSessionId: VICTIM_SESSION,
    authorizationRequestUrl: AUTHORIZATION_URL,
  });
  return verifier;
}

function legitimateCallback(
  overrides: Partial<{
    interactRef: string;
    hash: string;
    browserSessionId: string;
  }> = {},
) {
  const interactRef = overrides.interactRef ?? REAL_INTERACT_REF;
  return {
    interactRef,
    hash:
      overrides.hash ??
      calculateInteractionHash(
        CLIENT_NONCE,
        AS_NONCE,
        interactRef,
        AUTHORIZATION_URL,
      ),
    browserSessionId: overrides.browserSessionId ?? VICTIM_SESSION,
  };
}

describe("calculateInteractionHash", () => {
  it("按 LF 连接四段输入并输出无填充 base64url 的 SHA-256", () => {
    const hash = calculateInteractionHash(
      CLIENT_NONCE,
      AS_NONCE,
      REAL_INTERACT_REF,
      AUTHORIZATION_URL,
    );
    assert.equal(hash, KNOWN_ANSWER);
    assert.doesNotMatch(hash, /=/);
    assert.doesNotMatch(hash, /[+/]/);
  });

  it("任一输入字段变化都会改变哈希（绑定 interact_ref）", () => {
    const baseline = calculateInteractionHash(
      CLIENT_NONCE,
      AS_NONCE,
      REAL_INTERACT_REF,
      AUTHORIZATION_URL,
    );
    const swapped = calculateInteractionHash(
      CLIENT_NONCE,
      AS_NONCE,
      ATTACKER_INTERACT_REF,
      AUTHORIZATION_URL,
    );
    assert.notEqual(swapped, baseline);
  });
});

describe("constantTimeHashEquals", () => {
  it("相同哈希相等，兼容带 base64 填充的形式", () => {
    assert.equal(
      constantTimeHashEquals(KNOWN_ANSWER, `${KNOWN_ANSWER}===`),
      true,
    );
  });

  it("不同哈希返回 false，长度不同也安全返回 false", () => {
    assert.equal(
      constantTimeHashEquals(KNOWN_ANSWER, KNOWN_ANSWER.slice(0, -1)),
      false,
    );
    assert.equal(
      constantTimeHashEquals(
        KNOWN_ANSWER,
        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      ),
      false,
    );
  });
});

describe("GnapInteractionVerifier 合法流程", () => {
  it("哈希与浏览器会话均匹配时放行，并只在此时暴露续期用 interact_ref", () => {
    const verifier = registerVictimTransaction();
    assert.equal(verifier.getContinuationInteractRef(TX_ID), undefined);

    const result = verifier.verifyCallback(TX_ID, legitimateCallback());
    assert.deepEqual(result, {
      transactionId: TX_ID,
      interactRef: REAL_INTERACT_REF,
    });

    assert.equal(
      verifier.getContinuationInteractRef(TX_ID),
      REAL_INTERACT_REF,
    );
    assert.equal(verifier.getTransaction(TX_ID).callbackReceived, true);
  });
});

describe("GnapInteractionVerifier 攻击场景", () => {
  it("伪造/替换 interact_ref：重算哈希与 hash 参数不符，拒绝且不落状态", () => {
    const verifier = registerVictimTransaction();

    // 攻击者把 interact_ref 换成自己的，却保留受害者原始回调的 hash。
    const forgedCallback = {
      interactRef: ATTACKER_INTERACT_REF,
      hash: calculateInteractionHash(
        CLIENT_NONCE,
        AS_NONCE,
        REAL_INTERACT_REF,
        AUTHORIZATION_URL,
      ),
      browserSessionId: VICTIM_SESSION,
    };

    assert.throws(
      () => verifier.verifyCallback(TX_ID, forgedCallback),
      (error: unknown) =>
        error instanceof GnapVerificationError &&
        error.code === "INVALID_HASH",
    );

    // 失败不消费事务：合法回调随后仍可成功。
    const result = verifier.verifyCallback(TX_ID, legitimateCallback());
    assert.equal(result.interactRef, REAL_INTERACT_REF);
    assert.equal(
      verifier.getContinuationInteractRef(TX_ID),
      REAL_INTERACT_REF,
    );
  });

  it("跨会话回调：即便哈希正确，非登记浏览器会话也被拒绝", () => {
    const verifier = registerVictimTransaction();

    const crossSessionCallback = legitimateCallback({
      browserSessionId: ATTACKER_SESSION,
    });

    assert.throws(
      () => verifier.verifyCallback(TX_ID, crossSessionCallback),
      (error: unknown) =>
        error instanceof GnapVerificationError &&
        error.code === "CROSS_SESSION_CALLBACK",
    );

    assert.equal(verifier.getTransaction(TX_ID).callbackReceived, false);
    assert.equal(verifier.getContinuationInteractRef(TX_ID), undefined);
  });

  it("重复回调：同一事务第二次回调一律拒绝", () => {
    const verifier = registerVictimTransaction();

    const first = verifier.verifyCallback(TX_ID, legitimateCallback());
    assert.equal(first.interactRef, REAL_INTERACT_REF);

    // 重放完全相同的合法回调，或带攻击者 ref 的第二次回调，都应拒绝。
    assert.throws(
      () => verifier.verifyCallback(TX_ID, legitimateCallback()),
      (error: unknown) =>
        error instanceof GnapVerificationError &&
        error.code === "DUPLICATE_CALLBACK",
    );
    assert.throws(
      () =>
        verifier.verifyCallback(
          TX_ID,
          legitimateCallback({ interactRef: ATTACKER_INTERACT_REF }),
        ),
      (error: unknown) =>
        error instanceof GnapVerificationError &&
        error.code === "DUPLICATE_CALLBACK",
    );

    // 已接受的 interact_ref 不被后续尝试改变。
    assert.equal(
      verifier.getContinuationInteractRef(TX_ID),
      REAL_INTERACT_REF,
    );
  });

  it("未知事务与空字段输入被拒绝", () => {
    const verifier = registerVictimTransaction();

    assert.throws(
      () =>
        verifier.verifyCallback("does-not-exist", legitimateCallback()),
      (error: unknown) =>
        error instanceof GnapVerificationError &&
        error.code === "UNKNOWN_TRANSACTION",
    );

    assert.throws(
      () =>
        verifier.verifyCallback(TX_ID, {
          ...legitimateCallback(),
          hash: "",
        }),
      (error: unknown) =>
        error instanceof GnapVerificationError &&
        error.code === "INVALID_INPUT",
    );
  });
});
