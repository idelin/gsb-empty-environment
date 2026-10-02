/** 车载设备采集的原始轨迹点。heading 单位为度 [0, 360)，speed 为 m/s，alt 为米，t 为 epoch 毫秒。 */
export interface TrajectoryPoint {
  lat: number;
  lon: number;
  alt: number;
  speed: number;
  heading: number;
  t: number;
}

/** 自适应采样阈值。默认值针对车载 10Hz 采集调优。 */
export interface SamplerOptions {
  /** 直线段累计航向变化超过该值（度）即保留采样点。 */
  headingKeepDeg: number;
  /** 偏航角速度（度/秒）达到该值视为急转弯，进入密集采样。 */
  sharpTurnRateDegS: number;
  /** 急转弯时的航向采样步长（度），越小越密集。 */
  sharpTurnHeadingDeg: number;
  /** 高程落差（米）超过该值即保留采样点。 */
  elevationKeepM: number;
  /** 弦偏差（米）上限：被丢弃点到保留弦的最大垂直偏差，保证航迹形状。 */
  maxChordDeviationM: number;
  /** 弯道检测弧长（米）：在该弧长内航向反向变化达到阈值视为变道。 */
  laneChangeWindowM: number;
  /** 变道判定的最小航向摆幅（度）。 */
  laneChangeSwingDeg: number;
  /** 弦偏差检测的最小弦长（米）：短弦方向被定位噪声主导，暂不检测。 */
  minChordM: number;
  /** 采样点间最大时间间隔（毫秒），超长直线也周期性打点。 */
  maxGapMs: number;
  /** 采样点间最大距离（米）。 */
  maxGapM: number;
  /** 非急转弯时的最小采样间隔（毫秒），抑制抖动噪声。 */
  minGapMs: number;
}

export const DEFAULT_SAMPLER_OPTIONS: SamplerOptions = {
  headingKeepDeg: 2.0,
  sharpTurnRateDegS: 12.0,
  sharpTurnHeadingDeg: 0.5,
  elevationKeepM: 3.0,
  maxChordDeviationM: 4.0,
  minChordM: 10,
  laneChangeWindowM: 60.0,
  laneChangeSwingDeg: 1.0,
  maxGapMs: 60_000,
  maxGapM: 5_000,
  minGapMs: 250,
};
