import { describe, expect, test } from "bun:test";
import {
  buildSlices,
  checkConnectivity,
  isActiveAt,
} from "./temporalConnectivity";
import type { TemporalEdge } from "./temporalConnectivity";

const e = (from: string, to: string, start: number, end: number): TemporalEdge =>
  ({ from, to, start, end });

describe("isActiveAt 半开语义", () => {
  const edge = e("a", "b", 3, 7);
  test("起点含、终点不含", () => {
    expect(isActiveAt(edge, 2.999)).toBe(false);
    expect(isActiveAt(edge, 3)).toBe(true);
    expect(isActiveAt(edge, 5)).toBe(true);
    expect(isActiveAt(edge, 7)).toBe(false);
  });
});

describe("buildSlices 切片", () => {
  test("按所有有效端点切片，含查询端点", () => {
    const edges = [e("a", "b", 1, 4), e("b", "c", 2, 8)];
    const slices = buildSlices(edges, { source: "a", target: "c", start: 0, end: 10 });
    expect(slices.map((s) => [s.start, s.end])).toEqual([
      [0, 1], [1, 2], [2, 4], [4, 8], [8, 10],
    ]);
    expect(slices[0]!.activeEdgeIndices).toEqual([]);
    expect(slices[2]!.activeEdgeIndices).toEqual([0, 1]);
    expect(slices[3]!.activeEdgeIndices).toEqual([1]);
  });

  test("空查询区间不产生时间片", () => {
    const slices = buildSlices([e("a", "b", 0, 5)], { source: "a", target: "b", start: 5, end: 5 });
    expect(slices).toEqual([]);
    const reversed = buildSlices([], { source: "a", target: "b", start: 9, end: 3 });
    expect(reversed).toEqual([]);
  });

  test("忽略空生效窗的边", () => {
    const slices = buildSlices([e("a", "b", 4, 4), e("a", "b", 6, 2)], {
      source: "a", target: "b", start: 0, end: 10,
    });
    expect(slices.map((s) => [s.start, s.end])).toEqual([[0, 10]]);
    expect(slices[0]!.activeEdgeIndices).toEqual([]);
  });
});

describe("checkConnectivity", () => {
  test("全程连通：相邻有效段无缝衔接", () => {
    // a-b 全程；b-c 由 [0,5) 与 [5,10) 两段相邻拼成，中间无空隙
    const edges = [e("a", "b", 0, 10), e("b", "c", 0, 5), e("b", "c", 5, 10)];
    const r = checkConnectivity(edges, { source: "a", target: "c", start: 0, end: 10 });
    expect(r.connected).toBe(true);
    expect(r.firstFailure).toBeUndefined();
    expect(r.slices.length).toBe(2);
  });

  test("同时开闭：一条边关闭的同一时刻另一条边开启", () => {
    // t=5 时 b-c 关闭、b-d 与 d-c 同时开启，连通性不中断
    const edges = [
      e("a", "b", 0, 10),
      e("b", "c", 0, 5),
      e("b", "d", 5, 10),
      e("d", "c", 5, 10),
    ];
    const r = checkConnectivity(edges, { source: "a", target: "c", start: 0, end: 10 });
    expect(r.connected).toBe(true);
  });

  test("中间断开：只测两端会漏检，必须逐片核对", () => {
    // 两端 t=0 与 t=10 附近都连通，但 [5,6) 内 b-c 不通
    const edges = [
      e("a", "b", 0, 10),
      e("b", "c", 0, 5),
      e("b", "c", 6, 10),
    ];
    const r = checkConnectivity(edges, { source: "a", target: "c", start: 0, end: 10 });
    expect(r.connected).toBe(false);
    expect(r.firstFailure!.sliceStart).toBe(5);
    expect(r.firstFailure!.sliceEnd).toBe(6);
  });

  test("首次断开返回最小时间片", () => {
    // [2,3) 与 [7,9) 都断开，应报告 [2,3)
    const edges = [
      e("a", "b", 0, 2), e("a", "b", 3, 7), e("a", "b", 9, 10),
    ];
    const r = checkConnectivity(edges, { source: "a", target: "b", start: 0, end: 10 });
    expect(r.connected).toBe(false);
    expect([r.firstFailure!.sliceStart, r.firstFailure!.sliceEnd]).toEqual([2, 3]);
  });

  test("割证据：给出割两侧与跨割边及失效原因", () => {
    // a-b 恒通；b-c [0,5)；b-d [6,10)、d-c [6,10)。[5,6) 断开。
    const edges = [
      e("a", "b", 0, 10),
      e("b", "c", 0, 5),
      e("b", "d", 6, 10),
      e("d", "c", 6, 10),
    ];
    const r = checkConnectivity(edges, { source: "a", target: "c", start: 0, end: 10 });
    const f = r.firstFailure!;
    expect([f.sliceStart, f.sliceEnd]).toEqual([5, 6]);
    expect(f.reachable).toEqual(["a", "b"]);
    expect(f.unreachable).toEqual(["c", "d"]);
    // 跨割边：b-c 已过期，b-d 尚未生效
    const byIndex = new Map(f.cutEdges.map((c) => [c.edgeIndex, c.reason]));
    expect(byIndex.get(1)).toBe("expired");
    expect(byIndex.get(2)).toBe("not-yet-active");
    expect(f.cutEdges.every((c) => !isActiveAt(c.edge, f.sliceStart))).toBe(true);
  });

  test("空查询区间：vacuously 连通", () => {
    const r = checkConnectivity([], { source: "a", target: "b", start: 4, end: 4 });
    expect(r.connected).toBe(true);
    expect(r.slices).toEqual([]);
    expect(r.firstFailure).toBeUndefined();
    const reversed = checkConnectivity([], { source: "a", target: "b", start: 8, end: 2 });
    expect(reversed.connected).toBe(true);
  });

  test("source === target 平凡连通", () => {
    const r = checkConnectivity([], { source: "a", target: "a", start: 0, end: 10 });
    expect(r.connected).toBe(true);
  });

  test("查询区间外的事件不影响切片", () => {
    // 边在查询区间外有端点，不应产生额外切片
    const edges = [e("a", "b", -100, 100)];
    const r = checkConnectivity(edges, { source: "a", target: "b", start: 3, end: 4 });
    expect(r.connected).toBe(true);
    expect(r.slices.map((s) => [s.start, s.end])).toEqual([[3, 4]]);
  });

  test("边界：查询起点恰为边开启时刻，查询终点恰为边关闭时刻", () => {
    const edges = [e("a", "b", 5, 8)];
    const r = checkConnectivity(edges, { source: "a", target: "b", start: 5, end: 8 });
    expect(r.connected).toBe(true);
    // 查询区间超出边生效窗一点点即失败
    const r2 = checkConnectivity(edges, { source: "a", target: "b", start: 5, end: 8.5 });
    expect(r2.connected).toBe(false);
    expect([r2.firstFailure!.sliceStart, r2.firstFailure!.sliceEnd]).toEqual([8, 8.5]);
  });

  test("有向模式：反向边不可走", () => {
    const edges = [e("a", "b", 0, 10)];
    const undirected = checkConnectivity(edges, { source: "b", target: "a", start: 0, end: 10 });
    expect(undirected.connected).toBe(true);
    const directed = checkConnectivity(edges, { source: "b", target: "a", start: 0, end: 10 }, { directed: true });
    expect(directed.connected).toBe(false);
  });

  test("多条并行路径全部失效才断开", () => {
    const edges = [
      e("a", "b", 0, 10),
      e("a", "c", 0, 10), e("c", "b", 0, 10),
    ];
    const r = checkConnectivity(edges, { source: "a", target: "b", start: 0, end: 10 });
    expect(r.connected).toBe(true);
  });
});
