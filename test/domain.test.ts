import { test } from "node:test";
import assert from "node:assert/strict";
import { normalizeDomain, sameDomain } from "../index.ts";

test("normalizeDomain 把 Unicode 与混合大小写 Punycode 归一到同一小写 A-label", () => {
  const unicode = normalizeDomain("Bücher.example");
  const upperPuny = normalizeDomain("XN--BCHER-KVA.EXAMPLE");
  const lowerPuny = normalizeDomain("xn--bcher-kva.example");

  assert.equal(unicode, "xn--bcher-kva.example");
  assert.equal(upperPuny, "xn--bcher-kva.example");
  assert.equal(lowerPuny, "xn--bcher-kva.example");
  assert.equal(unicode, upperPuny);
});

test("normalizeDomain 处理日文 IDN、根点与空白", () => {
  assert.equal(normalizeDomain("例え.jp."), "xn--r8jz45g.jp");
  assert.equal(normalizeDomain("  EXAMPLE.com  "), "example.com");
});

test("normalizeDomain 拒绝非法标签", () => {
  assert.equal(normalizeDomain(""), null);
  assert.equal(normalizeDomain("bad domain.com"), null);
  assert.equal(normalizeDomain("-lead.example"), null);
  assert.equal(normalizeDomain("trail-.example"), null);
  assert.equal(normalizeDomain("a".repeat(64) + ".com"), null);
});

test("sameDomain 忽略大小写与 IDN 表现形式，杜绝 IDN 大小写绕过", () => {
  assert.equal(sameDomain("BÜCHER.Example", "xn--bcher-kva.example"), true);
  assert.equal(sameDomain("XN--BCHER-KVA.example", "xn--bcher-kva.EXAMPLE"), true);
  assert.equal(sameDomain("example.com", "other.com"), false);
  // 攻击者返回同 TLD 下的另一个对象必须判为不一致。
  assert.equal(sameDomain("victim.com", "attacker.com"), false);
});
