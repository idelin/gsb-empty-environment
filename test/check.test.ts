import assert from "node:assert/strict";
import { test } from "node:test";
import { checkChange, verifyCertificate } from "../src/index.ts";
import type { Certificate } from "../src/index.ts";
import {
  ANY,
  allow,
  and,
  deny,
  fwd,
  ip,
  labelIs,
  not,
  or,
  policy,
  port,
  prefix,
  rev,
  rule,
} from "../testkit/helpers.ts";

test("相同策略：通过且证书可独立验证", () => {
  const p = policy(
    rule(10, and(fwd, prefix("src", "10.0.0.0", 8)), allow(1)),
    rule(5, ANY, deny()),
  );
  const result = checkChange(p, p);
  assert.equal(result.verdict, "pass");
  assert.deepEqual(result.violations, []);
  const verify = verifyCertificate(result.certificate, p, p);
  assert.deepEqual(verify.errors, []);
  assert.equal(verify.ok, true);
});

test("只有经过多步标签迁移才出现的差别会被发现", () => {
  // 旧策略：标签 0 时允许正向报文（标签不变），反向一律拒绝。
  const oldPolicy = policy(rule(10, and(fwd, labelIs(7, 0)), allow()));
  // 新策略：正向报文把标签置为 1，标签 1 时允许反向报文。
  const newPolicy = policy(
    rule(10, and(fwd, labelIs(7, 0)), allow(1)), // 0
    rule(5, and(rev, labelIs(7, 1)), allow()), // 1
  );
  const result = checkChange(oldPolicy, newPolicy);
  assert.equal(result.verdict, "fail");
  // 单步检查（只看切换瞬间的 (0,0)）会漏掉：违规只出现在迁移后的 (0,1)。
  assert.deepEqual(result.violations, [
    {
      classId: 0,
      oldLabel: 0,
      newLabel: 1,
      direction: "reverse",
      newRule: 1,
      oldRule: -1,
    },
  ]);
  // 证书忠实记录违规，仍可通过独立验证。
  const verify = verifyCertificate(result.certificate, oldPolicy, newPolicy);
  assert.equal(verify.ok, true, verify.errors.join("\n"));
  // 标签对 (0,1) 的见证轨迹：先正向迁移标签，再反向放行。
  const pair = result.certificate.pairs.find(
    (p) => p.oldLabel === 0 && p.newLabel === 1,
  );
  assert.ok(pair);
  assert.deepEqual(
    pair.trace.map((s) => s.direction),
    ["forward"],
  );
});

test("反向报文按五元组的反向识别", () => {
  const oldPolicy = policy(rule(10, fwd, allow()));
  const newPolicy = policy(
    rule(10, fwd, allow()), // 0
    rule(5, prefix("src", "192.168.0.0", 16), allow()), // 1
  );
  const result = checkChange(oldPolicy, newPolicy);
  assert.equal(result.verdict, "fail");
  // 地址切出 3 个区间 → 3×3 = 9 个类别，端口不切分。
  assert.equal(result.stats.classes, 9);
  // 只有目的地址落在 192.168.0.0/16 的类别，其反向报文源地址才命中规则 1。
  assert.equal(result.violations.length, 3);
  for (const v of result.violations) {
    assert.equal(v.direction, "reverse");
    assert.equal(v.newRule, 1);
    const cls = result.certificate.classes[v.classId];
    assert.deepEqual(cls.dst, [ip("192.168.0.0"), ip("192.168.255.255")]);
  }
});

test("前缀边界：/8 扩到 /7 只在新覆盖的半区违规", () => {
  const oldPolicy = policy(rule(10, prefix("src", "10.0.0.0", 8), allow()));
  const newPolicy = policy(rule(10, prefix("src", "10.0.0.0", 7), allow()));
  const result = checkChange(oldPolicy, newPolicy);
  assert.equal(result.verdict, "fail");
  // 边界 {0, 10.0.0.0, 11.0.0.0, 12.0.0.0} → 4×4 = 16 个类别。
  assert.equal(result.stats.classes, 16);
  // 正向 4 类（源在 11/8）+ 反向 4 类（目的在 11/8）= 8 处违规。
  assert.equal(result.violations.length, 8);
  for (const v of result.violations) {
    const cls = result.certificate.classes[v.classId];
    const range = v.direction === "forward" ? cls.src : cls.dst;
    assert.deepEqual(range, [ip("11.0.0.0"), ip("11.255.255.255")]);
  }
  const verify = verifyCertificate(result.certificate, oldPolicy, newPolicy);
  assert.equal(verify.ok, true, verify.errors.join("\n"));
});

test("端口边界：扩区间只在新覆盖的端口段违规", () => {
  const oldPolicy = policy(rule(10, port("dst", 0, 1023), allow()));
  const newPolicy = policy(rule(10, port("dst", 0, 2047), allow()));
  const result = checkChange(oldPolicy, newPolicy);
  assert.equal(result.verdict, "fail");
  // 正向 3 类（目的端口 1024..2047）+ 反向 3 类（源端口 1024..2047）。
  assert.equal(result.violations.length, 6);
  for (const v of result.violations) {
    const cls = result.certificate.classes[v.classId];
    const range = v.direction === "forward" ? cls.dstPort : cls.srcPort;
    assert.deepEqual(range, [1024, 2047]);
  }
});

test("写法不同但授权相同的策略：通过", () => {
  const oldPolicy = policy(
    rule(10, and(not(rev), or(port("dst", 80, 80), port("dst", 443, 443))), allow()),
  );
  const newPolicy = policy(
    rule(10, and(fwd, port("dst", 80, 80)), allow()),
    rule(9, and(fwd, port("dst", 443, 443)), allow()),
  );
  const result = checkChange(oldPolicy, newPolicy);
  assert.equal(result.verdict, "pass");
  assert.deepEqual(result.violations, []);
  // 端口边界 {0,80,81,443,444} → 5×5 = 25 个类别。
  assert.equal(result.stats.classes, 25);
  const verify = verifyCertificate(result.certificate, oldPolicy, newPolicy);
  assert.equal(verify.ok, true, verify.errors.join("\n"));
});

test("证书经 JSON 序列化后仍可独立验证", () => {
  const oldPolicy = policy(
    rule(10, and(not(rev), or(port("dst", 80, 80), port("dst", 443, 443))), allow()),
  );
  const newPolicy = policy(
    rule(10, and(fwd, port("dst", 80, 80)), allow()),
    rule(9, and(fwd, port("dst", 443, 443)), allow()),
  );
  const result = checkChange(oldPolicy, newPolicy);
  const revived = JSON.parse(JSON.stringify(result.certificate)) as Certificate;
  const verify = verifyCertificate(revived, oldPolicy, newPolicy);
  assert.equal(verify.ok, true, verify.errors.join("\n"));
});

test("篡改证书会被独立验证拒绝", () => {
  const oldPolicy = policy(rule(10, and(fwd, labelIs(7, 0)), allow()));
  const newPolicy = policy(
    rule(10, and(fwd, labelIs(7, 0)), allow(1)),
    rule(5, and(rev, labelIs(7, 1)), allow()),
  );
  const result = checkChange(oldPolicy, newPolicy);
  const base = JSON.parse(JSON.stringify(result.certificate)) as Certificate;

  const missingClass = JSON.parse(JSON.stringify(base)) as Certificate;
  missingClass.classes.pop();
  assert.equal(verifyCertificate(missingClass, oldPolicy, newPolicy).ok, false);

  const wrongPair = JSON.parse(JSON.stringify(base)) as Certificate;
  wrongPair.pairs[0].newLabel = (wrongPair.pairs[0].newLabel + 1) % 8;
  assert.equal(verifyCertificate(wrongPair, oldPolicy, newPolicy).ok, false);

  const wrongStep = JSON.parse(JSON.stringify(base)) as Certificate;
  const traced = wrongStep.pairs.find((p) => p.trace.length > 0);
  assert.ok(traced);
  traced.trace[0].oldOutcome.allow = !traced.trace[0].oldOutcome.allow;
  assert.equal(verifyCertificate(wrongStep, oldPolicy, newPolicy).ok, false);

  const hiddenViolation = JSON.parse(JSON.stringify(base)) as Certificate;
  hiddenViolation.violations = [];
  assert.equal(verifyCertificate(hiddenViolation, oldPolicy, newPolicy).ok, false);
});

test("可达标签对从 (0,0) 出发且旧标签只能由旧策略推进", () => {
  // 旧策略从不设置标签 → 所有可达标签对的旧标签恒为 0。
  const oldPolicy = policy(rule(10, ANY, allow()));
  const newPolicy = policy(
    rule(10, and(fwd, labelIs(7, 0)), allow(3)),
    rule(9, ANY, allow()),
  );
  const result = checkChange(oldPolicy, newPolicy);
  assert.equal(result.verdict, "pass");
  assert.ok(result.certificate.pairs.length > 1);
  for (const pair of result.certificate.pairs) {
    assert.equal(pair.oldLabel, 0);
  }
  // 每个类别都包含初始标签对 (0,0)。
  const seen = new Set(
    result.certificate.pairs
      .filter((p) => p.oldLabel === 0 && p.newLabel === 0)
      .map((p) => p.classId),
  );
  assert.equal(seen.size, result.stats.classes);
  const verify = verifyCertificate(result.certificate, oldPolicy, newPolicy);
  assert.equal(verify.ok, true, verify.errors.join("\n"));
});

test("权限收缩不算违规", () => {
  const oldPolicy = policy(rule(10, ANY, allow()));
  const newPolicy = policy(rule(10, fwd, allow()));
  const result = checkChange(oldPolicy, newPolicy);
  assert.equal(result.verdict, "pass");
  assert.deepEqual(result.violations, []);
});
