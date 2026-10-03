/** 轨迹采样点：t 为 Unix 毫秒时间戳，x/y 为平面坐标（米），floor 为楼层（可选）。 */
export interface TrackPoint {
  t: number;
  x: number;
  y: number;
  floor?: number;
  label?: string;
}

/** 单实体轨迹：points 必须按 t 升序。 */
export interface Trajectory {
  entityId: string;
  points: TrackPoint[];
}

/** 一段精确相交区间（距离 <= 阈值的连续时段）。 */
export interface ContactInterval {
  start: number;
  end: number;
  minDistance: number;
  minDistanceAt: number;
}

/** 地点（柜台/区域）定义，用于把坐标解析为可读位置。 */
export interface Zone {
  name: string;
  x: number;
  y: number;
  floor?: number;
  radius: number;
}

/** 索引条目。 */
export interface IndexEntry {
  entityId: string;
  t: number;
  x: number;
  y: number;
  floor?: number;
}
