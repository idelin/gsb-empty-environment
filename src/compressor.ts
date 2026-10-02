import { ByteWriter } from './codec.ts';
import { crossTrackM, haversineM, headingDeltaDeg } from './geo.ts';
import {
  DEFAULT_SAMPLER_OPTIONS,
  type SamplerOptions,
  type TrajectoryPoint,
} from './types.ts';

export const MAGIC = 0x54524a31; // "TRJ1"
const FLAG_HEADING_LITERAL = 1 << 0;
const FLAG_ALT_LITERAL = 1 << 1;
const FLAG_SPEED_LITERAL = 1 << 2;
const FLAG_T_LITERAL = 1 << 3;
const MAX_PENDING = 512;

const LATLON_SCALE = 1e7; // 1e-7 度 ≈ 1.1cm，远低于车载 GPS 噪声
const HEADING_SCALE = 100; // 百分之一度
const ALT_SCALE = 100; // 厘米
const SPEED_SCALE = 1000; // 毫米/秒

/** value 是否落在 1/scale 整数网格上（可无损 delta 编码）。 */
function fitsScale(value: number, scale: number): boolean {
  return Math.round(value * scale) / scale === value;
}

/**
 * 流式轨迹压缩器。
 * 依据偏航角速度、净航向变化、高程落差、弦偏差与变道特征自适应采样：
 * 直线加速时仅保留起止点，急转弯/变道时自动加密，且保证被丢弃点相对
 * 保留折线的垂直偏差不超过 maxChordDeviationM（短弦邻域 minChordM 除外）。
 *
 * 编码采用整数网格状态递推：heading/speed/alt/t 在网格值上逐位无损；
 * 非网格值自动回退 float64 字面量，依然无损。经纬度量化到 1e-7°。
 */
export class TrajectoryCompressor {
  private readonly opts: SamplerOptions;
  private readonly body = new ByteWriter();
  private lastKept: TrajectoryPoint | null = null;
  private prev: TrajectoryPoint | null = null;
  private pending: TrajectoryPoint[] = [];
  private laneWindow: TrajectoryPoint[] = [];
  private keptCount = 0;
  private totalCount = 0;
  // 整数网格编码状态（与解码器严格同构）
  private encPrev: TrajectoryPoint | null = null;
  private latInt = 0;
  private lonInt = 0;
  private altInt = 0;
  private speedInt = 0;
  private headingInt = 0;

  constructor(options: Partial<SamplerOptions> = {}) {
    this.opts = { ...DEFAULT_SAMPLER_OPTIONS, ...options };
  }

  get stats(): { total: number; kept: number } {
    return { total: this.totalCount, kept: this.keptCount };
  }

  push(p: TrajectoryPoint): void {
    this.totalCount++;
    if (this.lastKept === null) {
      this.keep(p);
      this.prev = p;
      return;
    }
    const prev = this.prev!;
    const lastKept = this.lastKept;
    const dtMs = p.t - prev.t;
    const stepHeading = Math.abs(headingDeltaDeg(prev.heading, p.heading));
    const yawRateDegS = dtMs > 0 ? (stepHeading / dtMs) * 1000 : 0;
    const sharp = yawRateDegS >= this.opts.sharpTurnRateDegS;
    this.pending.push(p);
    this.pushLaneWindow(p);

    let target: TrajectoryPoint | null = null;
    let forced = false;

    // 1) 航迹形状保证：弦偏差越限时保留偏差最大的中间点
    const breach = this.chordBreach(p);
    if (breach !== null) {
      target = breach;
      forced = true;
    }
    // 2) 相对最后保留点的净航向变化（急转弯时使用更细步长 → 密集采样）。
    //    净变化对独立分布的航向噪声天然免疫，直线段不会误触发。
    const headingStep = sharp
      ? this.opts.sharpTurnHeadingDeg
      : this.opts.headingKeepDeg;
    if (
      target === null &&
      Math.abs(headingDeltaDeg(lastKept.heading, p.heading)) >= headingStep
    ) {
      target = p;
    }
    // 3) 变道：弧长窗口内航向先偏后回，保留摆幅极值点
    if (target === null) {
      const swing = this.laneChangeExtremum();
      if (swing !== null && swing !== lastKept) target = swing;
    }
    // 4) 高程落差
    if (
      target === null &&
      Math.abs(p.alt - lastKept.alt) >= this.opts.elevationKeepM
    ) {
      target = p;
    }
    // 5) 超长间隔兜底 / 缓冲区上限
    if (
      target === null &&
      (p.t - lastKept.t >= this.opts.maxGapMs ||
        haversineM(lastKept, p) >= this.opts.maxGapM ||
        this.pending.length >= MAX_PENDING)
    ) {
      target = p;
      forced = true;
    }

    // 非强制采样受最小间隔约束（急转弯除外）
    if (
      target !== null &&
      !forced &&
      !sharp &&
      p.t - lastKept.t < this.opts.minGapMs
    ) {
      target = null;
    }

    if (target !== null) this.keep(target);
    this.prev = p;
  }

  /** 结束输入，保留末尾点并输出完整数据包。 */
  flush(): Uint8Array {
    if (this.prev !== null && this.prev !== this.lastKept) {
      this.keep(this.prev);
    }
    const out = new ByteWriter();
    out.pushByte((MAGIC >>> 24) & 0xff);
    out.pushByte((MAGIC >>> 16) & 0xff);
    out.pushByte((MAGIC >>> 8) & 0xff);
    out.pushByte(MAGIC & 0xff);
    out.pushUvarint(this.keptCount);
    out.pushBytes(this.body.toUint8Array());
    return out.toUint8Array();
  }

  private keep(p: TrajectoryPoint): void {
    // 递归保证形状：p 之前的未保留点若相对弦 lastKept→p 越限，先保留偏差最大者
    if (this.lastKept !== null) {
      const idx = this.pending.indexOf(p);
      if (idx > 0 && haversineM(this.lastKept, p) >= this.opts.minChordM) {
        let worst: TrajectoryPoint | null = null;
        let worstDev = this.opts.maxChordDeviationM;
        for (let i = 0; i < idx; i++) {
          const dev = crossTrackM(this.pending[i], this.lastKept, p);
          if (dev > worstDev) {
            worstDev = dev;
            worst = this.pending[i];
          }
        }
        if (worst !== null) this.keep(worst);
      }
    }
    this.encodePoint(p);
    this.keptCount++;
    this.lastKept = p;
    const idx = this.pending.indexOf(p);
    this.pending = idx >= 0 ? this.pending.slice(idx + 1) : [];
  }

  private chordBreach(p: TrajectoryPoint): TrajectoryPoint | null {
    if (this.pending.length < 2) return null;
    // 弦长过短时方向被定位噪声主导，跳过（点都在 lastKept 的 minChordM 邻域内）
    if (haversineM(this.lastKept!, p) < this.opts.minChordM) return null;
    let worst: TrajectoryPoint | null = null;
    let worstDev = this.opts.maxChordDeviationM;
    // p 是 pending 末尾；检查其前的中间点相对弦 lastKept→p 的偏差
    for (let i = 0; i < this.pending.length - 1; i++) {
      const dev = crossTrackM(this.pending[i], this.lastKept!, p);
      if (dev > worstDev) {
        worstDev = dev;
        worst = this.pending[i];
      }
    }
    return worst;
  }

  private pushLaneWindow(p: TrajectoryPoint): void {
    this.laneWindow.push(p);
    let dist = 0;
    for (let i = this.laneWindow.length - 1; i > 0; i--) {
      dist += haversineM(this.laneWindow[i - 1], this.laneWindow[i]);
      if (dist > this.opts.laneChangeWindowM) {
        this.laneWindow.splice(0, i);
        break;
      }
    }
  }

  /** 窗口内航向相对首点的摆幅 ≥ 2×阈值时，返回摆幅极值点（变道特征）。 */
  private laneChangeExtremum(): TrajectoryPoint | null {
    const w = this.laneWindow;
    if (w.length < 3) return null;
    const base = w[0].heading;
    let min = 0;
    let max = 0;
    let extremum: TrajectoryPoint = w[0];
    let extremumAbs = 0;
    for (const q of w) {
      const rel = headingDeltaDeg(base, q.heading);
      if (rel < min) min = rel;
      if (rel > max) max = rel;
      if (Math.abs(rel) > extremumAbs) {
        extremumAbs = Math.abs(rel);
        extremum = q;
      }
    }
    if (max - min >= 2 * this.opts.laneChangeSwingDeg) {
      this.laneWindow = [];
      return extremum;
    }
    return null;
  }

  private encodePoint(p: TrajectoryPoint): void {
    const prev = this.encPrev;
    if (prev === null) {
      this.body.pushF64(p.lat);
      this.body.pushF64(p.lon);
      this.body.pushF64(p.alt);
      this.body.pushF64(p.speed);
      this.body.pushF64(p.heading);
      this.body.pushF64(p.t);
    } else {
      let flags = 0;
      const headingLiteral = !fitsScale(p.heading, HEADING_SCALE);
      const altLiteral = !fitsScale(p.alt, ALT_SCALE);
      const speedLiteral = !fitsScale(p.speed, SPEED_SCALE);
      const tLiteral = !Number.isInteger(p.t) || !Number.isInteger(prev.t);
      if (headingLiteral) flags |= FLAG_HEADING_LITERAL;
      if (altLiteral) flags |= FLAG_ALT_LITERAL;
      if (speedLiteral) flags |= FLAG_SPEED_LITERAL;
      if (tLiteral) flags |= FLAG_T_LITERAL;
      this.body.pushByte(flags);

      const latInt = Math.round(p.lat * LATLON_SCALE);
      const lonInt = Math.round(p.lon * LATLON_SCALE);
      this.body.pushSvarint(latInt - this.latInt);
      this.body.pushSvarint(lonInt - this.lonInt);

      if (tLiteral) this.body.pushF64(p.t);
      else this.body.pushSvarint(p.t - prev.t);

      if (altLiteral) this.body.pushF64(p.alt);
      else this.body.pushSvarint(Math.round(p.alt * ALT_SCALE) - this.altInt);

      if (speedLiteral) this.body.pushF64(p.speed);
      else this.body.pushSvarint(Math.round(p.speed * SPEED_SCALE) - this.speedInt);

      if (headingLiteral) this.body.pushF64(p.heading);
      else {
        this.body.pushSvarint(Math.round(p.heading * HEADING_SCALE) - this.headingInt);
      }
    }
    // 更新整数网格状态（与解码器完全同构，保证逐位一致）
    this.latInt = Math.round(p.lat * LATLON_SCALE);
    this.lonInt = Math.round(p.lon * LATLON_SCALE);
    this.altInt = Math.round(p.alt * ALT_SCALE);
    this.speedInt = Math.round(p.speed * SPEED_SCALE);
    this.headingInt = Math.round(p.heading * HEADING_SCALE);
    this.encPrev = p;
  }
}

/** 一次性压缩整段轨迹（内部即流式实现）。 */
export function compressTrajectory(
  points: Iterable<TrajectoryPoint>,
  options: Partial<SamplerOptions> = {},
): Uint8Array {
  const c = new TrajectoryCompressor(options);
  for (const p of points) c.push(p);
  return c.flush();
}
