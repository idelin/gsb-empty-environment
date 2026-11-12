import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  EchIdentityGuard,
  matchHostname,
  type EchHandshakeResult,
} from "./ech-identity.ts";

const INNER_SNI = "secret.example.com";
const PUBLIC_NAME = "front.example.net";

const innerCert = { dnsNames: [INNER_SNI] };
const publicNameCert = { dnsNames: [PUBLIC_NAME] };
const attackerCert = { dnsNames: ["evil.attacker.example"] };

const RETRY_CONFIGS = new Uint8Array([1, 2, 3]);

function rejected(
  peerCertificate: { dnsNames: string[] },
  retryConfigs: unknown = RETRY_CONFIGS,
): EchHandshakeResult {
  return { echAccepted: false, peerCertificate, retryConfigs };
}

describe("matchHostname", () => {
  it("matches exact names case-insensitively", () => {
    assert.equal(matchHostname("Example.COM", "example.com"), true);
    assert.equal(matchHostname("example.com", "example.com.evil"), false);
  });

  it("matches a single wildcard label only", () => {
    assert.equal(matchHostname("*.example.com", "a.example.com"), true);
    assert.equal(matchHostname("*.example.com", "a.b.example.com"), false);
    assert.equal(matchHostname("*.example.com", "example.com"), false);
  });
});

describe("EchIdentityGuard", () => {
  it("接受：证书对内层 SNI 有效时放行并允许应用数据", () => {
    const guard = new EchIdentityGuard({
      innerSni: INNER_SNI,
      publicName: PUBLIC_NAME,
    });
    const decision = guard.onHandshakeComplete({
      echAccepted: true,
      peerCertificate: innerCert,
    });
    assert.deepEqual(decision, { action: "proceed" });
    assert.doesNotThrow(() => guard.assertApplicationDataAllowed());
  });

  it("接受：证书仅对 public_name 有效（冒充内层主机）时中止", () => {
    const guard = new EchIdentityGuard({
      innerSni: INNER_SNI,
      publicName: PUBLIC_NAME,
    });
    const decision = guard.onHandshakeComplete({
      echAccepted: true,
      peerCertificate: publicNameCert,
    });
    assert.equal(decision.action, "abort");
    assert.match(
      decision.action === "abort" ? decision.reason : "",
      /inner SNI/,
    );
    assert.throws(() => guard.assertApplicationDataAllowed());
  });

  it("拒绝 + public_name 证书：采用 retry_configs，但禁止发送应用数据", () => {
    const guard = new EchIdentityGuard({
      innerSni: INNER_SNI,
      publicName: PUBLIC_NAME,
    });
    const decision = guard.onHandshakeComplete(rejected(publicNameCert));
    assert.deepEqual(decision, {
      action: "retry",
      retryConfigs: RETRY_CONFIGS,
    });
    assert.throws(
      () => guard.assertApplicationDataAllowed(),
      /must not be sent/,
    );
  });

  it("拒绝 + 内层名证书：不按 public_name 通过验证，中止且不采用 retry_configs", () => {
    const guard = new EchIdentityGuard({
      innerSni: INNER_SNI,
      publicName: PUBLIC_NAME,
    });
    const decision = guard.onHandshakeComplete(rejected(innerCert));
    assert.equal(decision.action, "abort");
    assert.match(
      decision.action === "abort" ? decision.reason : "",
      /public_name/,
    );
    assert.equal(guard.retriesRemaining, EchIdentityGuard.MAX_RETRIES);
    assert.equal(guard.nextConnectionPlan(decision), null);
  });

  it("拒绝 + 攻击者证书：同样中止", () => {
    const guard = new EchIdentityGuard({
      innerSni: INNER_SNI,
      publicName: PUBLIC_NAME,
    });
    const decision = guard.onHandshakeComplete(rejected(attackerCert));
    assert.equal(decision.action, "abort");
  });

  it("二次拒绝：重试预算耗尽后即使证书对 public_name 有效也中止", () => {
    const guard = new EchIdentityGuard({
      innerSni: INNER_SNI,
      publicName: PUBLIC_NAME,
    });
    const first = guard.onHandshakeComplete(rejected(publicNameCert));
    assert.equal(first.action, "retry");
    assert.equal(guard.retriesRemaining, 0);

    const second = guard.onHandshakeComplete(
      rejected(publicNameCert, new Uint8Array([9, 9, 9])),
    );
    assert.equal(second.action, "abort");
    assert.match(
      second.action === "abort" ? second.reason : "",
      /retry budget/,
    );
    assert.equal(guard.nextConnectionPlan(second), null);
  });

  it("重试后接受：第二次握手携带 ECH 且证书对内层 SNI 有效则放行", () => {
    const guard = new EchIdentityGuard({
      innerSni: INNER_SNI,
      publicName: PUBLIC_NAME,
    });
    guard.onHandshakeComplete(rejected(publicNameCert));
    const decision = guard.onHandshakeComplete({
      echAccepted: true,
      peerCertificate: innerCert,
    });
    assert.deepEqual(decision, { action: "proceed" });
    assert.doesNotThrow(() => guard.assertApplicationDataAllowed());
  });

  it("回落验证：重试计划始终携带 ECH，预算耗尽后不再给出任何连接计划", () => {
    const guard = new EchIdentityGuard({
      innerSni: INNER_SNI,
      publicName: PUBLIC_NAME,
    });
    const decision = guard.onHandshakeComplete(rejected(publicNameCert));
    const plan = guard.nextConnectionPlan(decision);
    assert.deepEqual(plan, { ech: true, retryConfigs: RETRY_CONFIGS });

    const exhausted = guard.onHandshakeComplete(rejected(publicNameCert));
    assert.equal(exhausted.action, "abort");
    assert.equal(guard.nextConnectionPlan(exhausted), null);
    // 组件不存在任何产生 ech:false 连接计划的路径。
    for (const d of [decision, exhausted]) {
      const p = guard.nextConnectionPlan(d);
      if (p !== null) assert.equal(p.ech, true);
    }
  });
});
