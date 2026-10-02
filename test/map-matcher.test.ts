import assert from 'node:assert/strict';
import test from 'node:test';
import { buildCityCanyonScenario } from '../src/city-canyon.ts';
import { haversineM } from '../src/geometry.ts';
import { findCandidates, matchTrace } from '../src/map-matcher.ts';
import { RoadNetwork } from '../src/road-network.ts';

test('路网：最短路连通性与路距计算', () => {
  const net = new RoadNetwork();
  // 简单 L 形路网：(0,0)-(0,1)-(1,1)（约 111km 每度纬度）
  const a = { lat: 0, lon: 0 };
  const b = { lat: 0, lon: 0.001 };
  const c = { lat: 0.001, lon: 0.001 };
  net.addSegment(a, b, 's0');
  net.addSegment(b, c, 's1');

  const fromB = net.shortestPathsFrom(1);
  assert.ok(Math.abs(fromB[0] - haversineM(a, b)) < 1e-6);
  assert.ok(Math.abs(fromB[2] - haversineM(b, c)) < 1e-6);
  assert.ok(Math.abs(fromB[0] + fromB[2] - (haversineM(a, b) + haversineM(b, c))) < 1e-6);
});

test('候选生成：按距离排序且包含真实路段', () => {
  const { network, gps, trueSegmentIds } = buildCityCanyonScenario(42);
  // 用半径足够大的搜索，漂移点的真实路段也应进入候选集。
  const cands = findCandidates(network, gps[20], 120, 8, 20);
  assert.ok(cands.length > 0);
  for (let k = 1; k < cands.length; k++) {
    assert.ok(cands[k - 1].projection.distanceM <= cands[k].projection.distanceM);
  }
  assert.ok(cands.some((cd) => cd.segmentId === trueSegmentIds[20]));
});

test('端到端：城市峡谷漂移轨迹被吸附回真实道路', () => {
  const { network, gps, trueSegmentIds, truePositions } = buildCityCanyonScenario(42);
  const result = matchTrace(network, gps);

  // 1) 路段序列还原：至少 95% 的点匹配到真实路段。
  const correct = result.matched.filter((m, i) => m.segmentId === trueSegmentIds[i]).length;
  const accuracy = correct / gps.length;
  assert.ok(
    accuracy >= 0.95,
    `segment accuracy ${(accuracy * 100).toFixed(1)}% < 95%`,
  );

  // 2) 误差显著降低：匹配后平均误差 < 原始误差的 30%。
  const mean = (xs: number[]) => xs.reduce((a, b) => a + b, 0) / xs.length;
  const rawMean = mean(gps.map((p, i) => haversineM(p, truePositions[i])));
  const matchedMean = mean(result.matched.map((m, i) => haversineM(m.matched, truePositions[i])));
  assert.ok(
    matchedMean < rawMean * 0.3,
    `matched mean ${matchedMean.toFixed(1)}m not < 30% of raw ${rawMean.toFixed(1)}m`,
  );

  // 3) 峡谷段（V3，样本 15~24）：漂移点全部吸附回 V3，而非平行的 V2/V4。
  for (let i = 15; i <= 24; i++) {
    const name = network.segment(result.matched[i].segmentId).name;
    assert.ok(name.startsWith('V3'), `sample ${i} matched to ${name}, expected V3-*`);
  }

  // 4) 匹配点必须落在道路中心线上（到所在路段投影距离为 0 级）。
  for (const m of result.matched) {
    const seg = network.segment(m.segmentId);
    const cands = findCandidates(network, m.matched, 1, 5, 20);
    assert.ok(
      cands.some((cd) => cd.segmentId === seg.id && cd.projection.distanceM < 0.5),
      `matched point not on segment ${seg.name}`,
    );
  }
});

test('端到端：不同随机种子下结果稳定', () => {
  for (const seed of [1, 7, 123]) {
    const { network, gps, trueSegmentIds } = buildCityCanyonScenario(seed);
    const result = matchTrace(network, gps);
    const correct = result.matched.filter((m, i) => m.segmentId === trueSegmentIds[i]).length;
    assert.ok(
      correct / gps.length >= 0.9,
      `seed ${seed}: accuracy ${((100 * correct) / gps.length).toFixed(1)}%`,
    );
  }
});
