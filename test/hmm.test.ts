import assert from 'node:assert/strict';
import test from 'node:test';
import { emissionLogProb, transitionLogProb, viterbi } from '../src/hmm.ts';
import type { Candidate } from '../src/types.ts';

const PARAMS = { sigma: 20, beta: 30, maxSpeedMps: 35 };

function cand(segmentId: number, emissionLogProbValue: number): Candidate {
  return {
    segmentId,
    projection: { point: { lat: 0, lon: 0 }, distanceM: 0, fraction: 0, alongM: 0 },
    emissionLogProb: emissionLogProbValue,
  };
}

test('发射概率：离路段越近概率越大，符合高斯衰减', () => {
  const near = emissionLogProb(5, PARAMS.sigma);
  const far = emissionLogProb(40, PARAMS.sigma);
  assert.ok(near > far);
  // 对数概率差应等于 (40²-5²)/(2σ²)
  const expected = (40 * 40 - 5 * 5) / (2 * PARAMS.sigma * PARAMS.sigma);
  assert.ok(Math.abs(near - far - expected) < 1e-9);
});

test('转移概率：路距与直线距一致时最大，偏离时指数衰减', () => {
  const perfect = transitionLogProb(100, 100, 10, PARAMS);
  const deviated = transitionLogProb(130, 100, 10, PARAMS);
  assert.equal(perfect, 0);
  assert.ok(Math.abs(deviated - -1) < 1e-9); // 偏差 30m / β=30 → -1
});

test('转移概率：超速与不连通均不可能', () => {
  // 200m / 2s = 100 m/s > 35 m/s
  assert.equal(transitionLogProb(200, 20, 2, PARAMS), -Infinity);
  // 不连通
  assert.equal(transitionLogProb(Infinity, 20, 2, PARAMS), -Infinity);
  // 合法速度
  assert.ok(Number.isFinite(transitionLogProb(60, 20, 2, PARAMS)));
});

test('Viterbi：全局最优胜过逐点贪心（拓扑纠正漂移点）', () => {
  // 3 个时刻，每层 2 个候选：路段 0（正确道路）与路段 1（漂移点旁的平行路）。
  // 中间时刻观测离路段 1 更近（发射概率高），但路段 0 的转移链更连贯。
  const layers: Candidate[][] = [
    [cand(0, -0.1), cand(1, -2.0)],
    [cand(0, -3.0), cand(1, -0.1)], // 漂移点：贪心会选路段 1
    [cand(0, -0.1), cand(1, -2.0)],
  ];
  // 转移：同路段 0 代价，跨路段 -50（拓扑上不可达的强惩罚）。
  const transition = (a: Candidate, b: Candidate) => (a.segmentId === b.segmentId ? 0 : -50);
  const { indices, logProb } = viterbi(layers, transition);

  assert.deepEqual(indices, [0, 0, 0]);
  assert.ok(Math.abs(logProb - (-0.1 - 3.0 - 0.1)) < 1e-9);
});

test('Viterbi：单点轨迹与空轨迹的边界', () => {
  const single = viterbi([[cand(0, -1), cand(1, -0.5)]], () => 0);
  assert.deepEqual(single.indices, [1]);
  assert.equal(single.logProb, -0.5);

  const empty = viterbi([], () => 0);
  assert.deepEqual(empty.indices, []);
});
