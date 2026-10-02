import test from "node:test";
import assert from "node:assert/strict";
import { deriveKeys, generateMasterKey } from "../src/crypto.ts";
import { TrajectoryIndex, type LocationPoint } from "../src/store.ts";

const HOUR = 60 * 60 * 1000;
const T0 = Date.UTC(2026, 0, 1, 0, 0, 0);

function makeIndex(kAnonymity = 3): TrajectoryIndex {
  return new TrajectoryIndex(deriveKeys(generateMasterKey()), {
    bucketSizeMs: HOUR,
    finePrecision: 3,
    coarsePrecision: 1,
    kAnonymity,
  });
}

function crowd(index: TrajectoryIndex, users: number, hours: number): void {
  let n = 0;
  for (let u = 0; u < users; u++) {
    for (let h = 0; h < hours; h++) {
      index.ingest({
        entityId: `crowd-${u}`,
        lat: 31.23 + (n % 7) * 0.001,
        lon: 121.47 + (n % 5) * 0.001,
        timestamp: T0 + h * HOUR + 60000,
      });
      n++;
    }
  }
}

test("写入后日常查询仅返回脱敏聚合热力，不含假名与精确坐标", () => {
  const index = makeIndex(3);
  crowd(index, 10, 4);
  const heat = index.heatmap(T0, T0 + 4 * HOUR);
  assert.ok(heat.length > 0);
  for (const cell of heat) {
    assert.match(cell.cellId, /^-?\d+\.\d:-?\d+\.\d$/);
    assert.ok(cell.count >= 3);
    assert.deepEqual(Object.keys(cell).sort(), ["cellId", "count"]);
  }
});

test("k-匿名阈值：稀疏网格被抑制", () => {
  const index = makeIndex(5);
  crowd(index, 10, 2);
  index.ingest({ entityId: "lonely", lat: 40.0, lon: 116.4, timestamp: T0 });
  const heat = index.heatmap(T0, T0 + 2 * HOUR);
  assert.ok(heat.every((c) => c.cellId !== "40.0:116.4"));
  assert.ok(heat.some((c) => c.cellId === "31.2:121.5"));
});

test("热力分布按时间窗过滤", () => {
  const index = makeIndex(1);
  index.ingest({ entityId: "a", lat: 31.2, lon: 121.4, timestamp: T0 + HOUR });
  index.ingest({ entityId: "b", lat: 40.0, lon: 116.4, timestamp: T0 + 10 * HOUR });
  const early = index.heatmap(T0, T0 + 2 * HOUR);
  assert.deepEqual(early, [{ cellId: "31.2:121.4", count: 1 }]);
});

test("定向还原：仅返回目标实体在授权时间窗内的轨迹", () => {
  const index = makeIndex(1);
  const trail: LocationPoint[] = [
    { entityId: "target", lat: 31.1, lon: 121.1, timestamp: T0 + 1 * HOUR },
    { entityId: "target", lat: 31.2, lon: 121.2, timestamp: T0 + 5 * HOUR },
    { entityId: "target", lat: 31.3, lon: 121.3, timestamp: T0 + 9 * HOUR },
    { entityId: "other", lat: 31.2, lon: 121.2, timestamp: T0 + 5 * HOUR },
  ];
  for (const p of trail) index.ingest(p);
  const revealed = index.reveal("target", T0 + 4 * HOUR, T0 + 8 * HOUR);
  assert.equal(revealed.length, 1);
  assert.deepEqual(revealed[0], { lat: 31.2, lon: 121.2, timestamp: T0 + 5 * HOUR });
});

test("定向还原：还原结果与原始写入完全一致且按时间排序", () => {
  const index = makeIndex(1);
  const points: LocationPoint[] = [
    { entityId: "target", lat: 31.33, lon: 121.11, timestamp: T0 + 3 * HOUR },
    { entityId: "target", lat: 31.11, lon: 121.33, timestamp: T0 + 1 * HOUR },
    { entityId: "target", lat: 31.22, lon: 121.22, timestamp: T0 + 2 * HOUR },
  ];
  for (const p of points) index.ingest(p);
  const revealed = index.reveal("target", T0, T0 + 24 * HOUR);
  assert.deepEqual(
    revealed.map((p) => p.lat),
    [31.11, 31.22, 31.33],
  );
});

test("假名跨时间桶不可链接", () => {
  const index = makeIndex(1);
  const p1 = index.inspectPseudonymOf("target", T0);
  const p2 = index.inspectPseudonymOf("target", T0 + HOUR);
  assert.notEqual(p1, p2);
});

test("非法坐标被拒绝", () => {
  const index = makeIndex(1);
  assert.throws(
    () => index.ingest({ entityId: "x", lat: 91, lon: 0, timestamp: T0 }),
    RangeError,
  );
});
