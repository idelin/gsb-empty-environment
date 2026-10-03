import { SpacetimeGridIndex } from "./grid_index.ts";
import type { TrajectoryPoint } from "./types.ts";

/**
 * 疾控流调验收场景：星光购物中心，2026-09-30 上午。
 * 两名确诊者（张某 A、李某 B）的轨迹由带柜台标签的航点构成，
 * 相邻航点之间线性插值。坐标单位：米；时间：Unix 毫秒。
 */

export const MALL_TIME_BASE = Date.parse("2026-09-30T10:00:00+08:00");
export const CASE_A = "确诊者A-张某";
export const CASE_B = "确诊者B-李某";
export const THRESHOLD_METERS = 10;

const MIN = 60_000;

function wp(minutes: number, x: number, y: number, label: string): TrajectoryPoint {
  return { t: MALL_TIME_BASE + minutes * MIN, x, y, z: 0, label };
}

/** 张某轨迹：化妆品 -> 珠宝 -> 奶茶 -> 生鲜 -> 出口。 */
export function trajectoryA(): TrajectoryPoint[] {
  return [
    wp(0, 0, 0, "化妆品专柜"),
    wp(8, 0, 0, "化妆品专柜"),
    wp(16, 40, 0, "珠宝专柜"),
    wp(24, 40, 30, "奶茶柜台"),
    wp(32, 40, 30, "奶茶柜台"),
    wp(40, 0, 30, "生鲜柜台"),
    wp(48, 0, 30, "生鲜柜台"),
    wp(56, 70, 15, "商场出口"),
  ];
}

/** 李某轨迹：店外 -> 化妆品 -> 珠宝 -> 奶茶 -> 生鲜 -> 出口。 */
export function trajectoryB(): TrajectoryPoint[] {
  return [
    wp(2, -40, 0, "店外停车场"),
    wp(6, 0, 0, "化妆品专柜"),
    wp(14, 0, 0, "化妆品专柜"),
    wp(22, 40, 0, "珠宝专柜"),
    wp(26, 40, 0, "珠宝专柜"),
    wp(33, 40, 30, "奶茶柜台"),
    wp(40, 40, 30, "奶茶柜台"),
    wp(46, 0, 30, "生鲜柜台"),
    wp(52, 0, 30, "生鲜柜台"),
    wp(60, 70, 15, "商场出口"),
  ];
}

/** 期望的三段相遇（分钟，相对 10:00），由轨迹几何精确推导。 */
export const EXPECTED_INTERVALS_MIN = [
  { start: 5, end: 10, label: "化妆品专柜" },
  { start: 30 + 2 / 3, end: 34, label: "奶茶柜台" },
  { start: 44.5, end: 48 + 10 / Math.sqrt(80.078125), label: "生鲜柜台" },
] as const;

export interface MallScenario {
  index: SpacetimeGridIndex;
  entityA: string;
  entityB: string;
  thresholdMeters: number;
}

export function buildMallScenario(): MallScenario {
  const index = new SpacetimeGridIndex({
    sliceDurationMs: 5 * MIN,
    cellSizeMeters: 20,
  });
  index.insert(CASE_A, trajectoryA());
  index.insert(CASE_B, trajectoryB());
  return { index, entityA: CASE_A, entityB: CASE_B, thresholdMeters: THRESHOLD_METERS };
}
