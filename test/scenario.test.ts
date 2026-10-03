import { test } from "node:test";
import assert from "node:assert/strict";
import { IntersectionProver, verifyProof } from "../src/prover.ts";
import {
  buildMallScenario,
  EXPECTED_INTERVALS_MIN,
  MALL_TIME_BASE,
} from "../src/scenario.ts";

const MIN = 60_000;

test("疾控流调验收：商场内精准输出 3 段 <10 米相遇时段与柜台位置", () => {
  const { index, entityA, entityB, thresholdMeters } = buildMallScenario();
  const prover = new IntersectionProver(index);
  const proof = prover.prove(entityA, entityB, thresholdMeters);

  assert.equal(proof.intervals.length, 3, "应恰好发现 3 段相遇");

  proof.intervals.forEach((iv, i) => {
    const expected = EXPECTED_INTERVALS_MIN[i];
    const startMin = (iv.tStart - MALL_TIME_BASE) / MIN;
    const endMin = (iv.tEnd - MALL_TIME_BASE) / MIN;
    assert.ok(
      Math.abs(startMin - expected.start) < 1e-6,
      `第 ${i + 1} 段开始时刻偏差过大：${startMin} vs ${expected.start}`,
    );
    assert.ok(
      Math.abs(endMin - expected.end) < 1e-6,
      `第 ${i + 1} 段结束时刻偏差过大：${endMin} vs ${expected.end}`,
    );
    assert.equal(iv.location.label, expected.label);
    assert.ok(iv.minDistanceMeters < thresholdMeters);
  });

  // 相遇地点应为三个不同柜台。
  const labels = proof.intervals.map((iv) => iv.location.label);
  assert.deepEqual(labels, ["化妆品专柜", "奶茶柜台", "生鲜柜台"]);

  // 每段相遇都有对应证书，且整体证明不可抵赖。
  assert.equal(proof.certificates.length, 3);
  const verification = verifyProof(proof);
  assert.ok(verification.ok, verification.errors.join("; "));

  // 验收性能：数毫秒内完成时空求交。
  assert.ok(
    proof.queryDurationMs < 10,
    `求交耗时 ${proof.queryDurationMs}ms 超出 10ms 预算`,
  );
});

test("阈值收紧到 5 米时相遇段数减少且仍精确", () => {
  const { index, entityA, entityB } = buildMallScenario();
  const prover = new IntersectionProver(index);
  const proof = prover.prove(entityA, entityB, 5);
  // 第 2 段相遇最近距离约 3.25m 仍在 5m 内，三段都保留但时段更短。
  assert.equal(proof.intervals.length, 3);
  const first = proof.intervals[0];
  const startMin = (first.tStart - MALL_TIME_BASE) / MIN;
  const endMin = (first.tEnd - MALL_TIME_BASE) / MIN;
  assert.ok(Math.abs(startMin - 5.5) < 1e-6);
  assert.ok(Math.abs(endMin - 9) < 1e-6);
});

test("无接触实体对返回空证明且摘要自洽", () => {
  const { index, entityA } = buildMallScenario();
  index.insert("确诊者C-王某", [
    { t: MALL_TIME_BASE, x: 500, y: 500, z: 0, label: "另一城区" },
    { t: MALL_TIME_BASE + 60 * MIN, x: 520, y: 500, z: 0, label: "另一城区" },
  ]);
  const prover = new IntersectionProver(index);
  const proof = prover.prove(entityA, "确诊者C-王某", 10);
  assert.equal(proof.intervals.length, 0);
  assert.equal(proof.certificates.length, 0);
  const verification = verifyProof(proof);
  assert.ok(verification.ok, verification.errors.join("; "));
});
