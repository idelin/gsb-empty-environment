/** 轨迹点：t 为 Unix 毫秒时间戳，x/y/z 为空间坐标（米），label 为可选位置标签（如柜台名）。 */
export interface TrajectoryPoint {
  t: number;
  x: number;
  y: number;
  z: number;
  label?: string;
}

/** 一次相遇接触时段。 */
export interface ContactInterval {
  tStart: number;
  tEnd: number;
  durationMs: number;
  minDistanceMeters: number;
  tClosest: number;
  location: { label: string; x: number; y: number; z: number };
}

export interface Vec3 {
  x: number;
  y: number;
  z: number;
}
