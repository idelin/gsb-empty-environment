import { NEIGHBORS8, inBounds } from './dem.ts';
import type { CellState, DEM, Grid, RainfallSeries, TickSnapshot } from './types.ts';

export interface SimulatorOptions {
  /** 数字高程模型。 */
  dem: DEM;
  /** 渗透率栅格（米/tick），缺省为全 0（不透水）。 */
  infiltration?: Grid;
  /** 降雨时序（mm/tick）。 */
  rainfall: RainfallSeries;
  /**
   * 单 tick 内水流交换系数 (0, 0.5]。
   * 越大漫延越快；0.5 为完全均衡化的稳定上限。
   */
  flowFactor?: number;
  /** 判定“被淹没”的积水深度阈值（米），默认 0.05m。 */
  floodThreshold?: number;
  /** 是否保存每个 tick 的历史快照（用于任意历史时刻查询），默认 true。 */
  keepHistory?: boolean;
}

/**
 * 时空栅格洪水蔓延仿真内核。
 *
 * 每个 tick 依次执行：
 *   1. 降雨：depth += rainfall[tick] (mm -> m)
 *   2. 入渗：depth -= infiltrationRate (m/tick)，下限 0
 *   3. 漫延：按水面高程差向 8 邻域低处重新分配水量（体积守恒）
 */
export class FloodSimulator {
  readonly dem: DEM;
  readonly infiltration: Grid;
  readonly rainfall: RainfallSeries;
  readonly flowFactor: number;
  readonly floodThreshold: number;

  private depth: Float64Array;
  private currentTick = 0;
  private history: Map<number, TickSnapshot> | null;

  constructor(options: SimulatorOptions) {
    this.dem = options.dem;
    const { rows, cols } = this.dem;
    this.infiltration = options.infiltration ?? {
      rows,
      cols,
      data: new Float64Array(rows * cols),
    };
    if (this.infiltration.rows !== rows || this.infiltration.cols !== cols) {
      throw new Error('渗透率栅格尺寸必须与 DEM 一致');
    }
    this.rainfall = options.rainfall;
    this.flowFactor = options.flowFactor ?? 0.25;
    if (!(this.flowFactor > 0 && this.flowFactor <= 0.5)) {
      throw new Error(`flowFactor 必须在 (0, 0.5] 内，当前: ${this.flowFactor}`);
    }
    this.floodThreshold = options.floodThreshold ?? 0.05;
    this.depth = new Float64Array(rows * cols);
    this.history = options.keepHistory === false ? null : new Map();
    this.recordSnapshot();
  }

  get tick(): number {
    return this.currentTick;
  }

  /** 当前积水深度栅格（只读视图）。 */
  get currentDepth(): Readonly<Float64Array> {
    return this.depth;
  }

  /** 推进一个 tick。返回该 tick 序号。 */
  step(): number {
    const { rows, cols, data: elev } = this.dem;
    const depth = this.depth;

    // 1. 降雨（mm -> m）
    const rainMm = this.rainfall.mmPerTick[this.currentTick] ?? 0;
    if (rainMm > 0) {
      const rainM = rainMm / 1000;
      for (let i = 0; i < depth.length; i++) depth[i] += rainM;
    }

    // 2. 入渗
    const inf = this.infiltration.data;
    for (let i = 0; i < depth.length; i++) {
      if (depth[i] > 0) depth[i] = Math.max(0, depth[i] - inf[i]);
    }

    // 3. 漫延：按水面高程差向低处分配（通量缓冲，保证体积守恒）
    const flux = new Float64Array(depth.length);
    const cellSize = this.dem.cellSize;
    for (let r = 0; r < rows; r++) {
      for (let c = 0; c < cols; c++) {
        const i = r * cols + c;
        if (depth[i] <= 0) continue;
        const level = elev[i] + depth[i];
        // 收集更低的邻居及高差
        let totalDrop = 0;
        const drops: number[] = [];
        const targets: number[] = [];
        for (const [dr, dc] of NEIGHBORS8) {
          const nr = r + dr;
          const nc = c + dc;
          if (!inBounds(this.dem, nr, nc)) continue;
          const j = nr * cols + nc;
          const drop = level - (elev[j] + depth[j]);
          if (drop > 0) {
            // 对角方向距离更远，通量按距离衰减
            const dist = dr !== 0 && dc !== 0 ? Math.SQRT2 * cellSize : cellSize;
            const w = drop / dist;
            drops.push(w);
            targets.push(j);
            totalDrop += w;
          }
        }
        if (totalDrop <= 0) continue;
        // 可分配水量：不超过自身水深，且分配比例受 flowFactor 限制
        const movable = depth[i] * this.flowFactor;
        for (let k = 0; k < targets.length; k++) {
          const share = movable * (drops[k] / totalDrop);
          flux[targets[k]] += share;
          flux[i] -= share;
        }
      }
    }
    for (let i = 0; i < depth.length; i++) {
      depth[i] = Math.max(0, depth[i] + flux[i]);
    }

    this.currentTick += 1;
    this.recordSnapshot();
    return this.currentTick;
  }

  /** 推进 n 个 tick。 */
  run(ticks: number): number {
    for (let i = 0; i < ticks; i++) this.step();
    return this.currentTick;
  }

  /** 任意历史时刻的积水深度栅格；越界时抛错。 */
  depthAt(tick: number): Readonly<Float64Array> {
    if (tick === this.currentTick) return this.depth;
    if (!this.history) {
      throw new Error('未开启历史快照（keepHistory=false），仅可查询当前时刻');
    }
    const snap = this.history.get(tick);
    if (!snap) {
      throw new Error(`无 tick=${tick} 的历史快照（有效范围 0..${this.currentTick}）`);
    }
    return snap.depth;
  }

  /** 指定 tick 某单元状态。 */
  cellStateAt(tick: number, row: number, col: number): CellState {
    if (!inBounds(this.dem, row, col)) {
      throw new Error(`单元越界: (${row},${col})`);
    }
    const depth = this.depthAt(tick);
    const i = row * this.dem.cols + col;
    const elevation = this.dem.data[i];
    const waterDepth = depth[i];
    return { row, col, elevation, waterDepth, waterLevel: elevation + waterDepth };
  }

  /** 指定 tick 的淹没掩码（depth > floodThreshold）。 */
  floodedMaskAt(tick: number): Uint8Array {
    const depth = this.depthAt(tick);
    const mask = new Uint8Array(depth.length);
    for (let i = 0; i < depth.length; i++) {
      if (depth[i] > this.floodThreshold) mask[i] = 1;
    }
    return mask;
  }

  /** 当前水体总体积（立方米，depth * cellSize^2 求和）。 */
  totalWaterVolume(): number {
    const a = this.dem.cellSize * this.dem.cellSize;
    let v = 0;
    for (let i = 0; i < this.depth.length; i++) v += this.depth[i];
    return v * a;
  }

  private recordSnapshot(): void {
    if (!this.history) return;
    this.history.set(this.currentTick, {
      tick: this.currentTick,
      depth: this.depth.slice(),
    });
  }
}
