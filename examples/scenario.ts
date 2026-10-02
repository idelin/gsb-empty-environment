import type { TrajectoryPoint } from '../src/types.ts';

/** 确定性伪随机数（mulberry32），保证测试可复现。 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface LabeledPoint extends TrajectoryPoint {
  segment: string;
}

const BASE_LAT = 31.2304;
const BASE_LON = 121.4737;
const M_PER_DEG_LAT = 111_320;
const M_PER_DEG_LON = 111_320 * Math.cos((BASE_LAT * Math.PI) / 180);

export function metersToLatLon(x: number, y: number): { lat: number; lon: number } {
  return { lat: BASE_LAT + y / M_PER_DEG_LAT, lon: BASE_LON + x / M_PER_DEG_LON };
}

/** 车辆运动学积分器：heading 0=北 90=东，单位米/秒。 */
class Vehicle {
  x = 0;
  y = 0;
  alt = 8;
  speed = 0;
  heading = 90;
  t: number;

  constructor(startT: number) {
    this.t = startT;
  }

  step(dt: number, yawRateDegS: number, accel: number, altRate: number): void {
    this.heading = (this.heading + yawRateDegS * dt + 360) % 360;
    this.speed = Math.max(0, this.speed + accel * dt);
    this.alt += altRate * dt;
    const rad = (this.heading * Math.PI) / 180;
    this.x += this.speed * Math.sin(rad) * dt;
    this.y += this.speed * Math.cos(rad) * dt;
    this.t += dt * 1000;
  }

  point(segment: string): LabeledPoint {
    const { lat, lon } = metersToLatLon(this.x, this.y);
    return { lat, lon, alt: this.alt, speed: this.speed, heading: this.heading, t: Math.round(this.t), segment };
  }
}

export interface ScenarioOptions {
  hz?: number;
  noiseM?: number;
  headingNoiseDeg?: number;
  seed?: number;
}

/**
 * 生成一段混合工况车载轨迹：
 * 直线加速 → 90° 急转弯 → 匀速直线 → 变道 → S 弯 → 爬坡。
 */
export function generateScenario(opts: ScenarioOptions = {}): LabeledPoint[] {
  const hz = opts.hz ?? 10;
  const dt = 1 / hz;
  const rand = mulberry32(opts.seed ?? 42);
  const noiseM = opts.noiseM ?? 0;
  const headingNoise = opts.headingNoiseDeg ?? 0;
  const v = new Vehicle(1_759_000_000_000);
  const out: LabeledPoint[] = [];

  const run = (
    segment: string,
    seconds: number,
    control: (elapsed: number) => { yawRate?: number; accel?: number; altRate?: number },
  ): void => {
    const steps = Math.round(seconds * hz);
    for (let i = 0; i < steps; i++) {
      const c = control(i * dt);
      v.step(dt, c.yawRate ?? 0, c.accel ?? 0, c.altRate ?? 0);
      const p = v.point(segment);
      if (noiseM > 0) {
        p.lat += ((rand() - 0.5) * 2 * noiseM) / M_PER_DEG_LAT;
        p.lon += ((rand() - 0.5) * 2 * noiseM) / M_PER_DEG_LON;
      }
      if (headingNoise > 0) {
        p.heading = (p.heading + (rand() - 0.5) * 2 * headingNoise + 360) % 360;
      }
      out.push(p);
    }
  };

  // 1) 直线加速：0 → 30 m/s，航向恒定（应仅保留起止点）
  run('straight-accel', 30, () => ({ accel: 1.0 }));
  // 2) 90° 急转弯（路口右转）：偏航 30°/s（应密集采样）
  run('sharp-turn', 3, () => ({ yawRate: 30, accel: -8 }));
  // 3) 匀速直线巡航
  run('cruise', 10, () => ({}));
  // 4) 变道：航向 ±3° 正弦摆动
  run('lane-change', 4, (e) => ({ yawRate: 4.5 * Math.cos((e / 4) * 2 * Math.PI) }));
  // 5) S 弯
  run('s-curve', 8, (e) => ({ yawRate: 10 * Math.sin((e / 8) * 2 * Math.PI) }));
  // 6) 爬坡：高程 +20m
  run('hill-climb', 10, () => ({ altRate: 2 }));

  return out;
}
