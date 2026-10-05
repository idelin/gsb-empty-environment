/**
 * 分块图像金字塔（mip pyramid）。
 *
 * - 每个父像素由子层 2x2 足迹外扩滤波核半径 r 的窗口加权生成（可分离核）。
 * - 层按块（block）划分；局部编辑只把精确受影响的块标记为脏并入队。
 * - 失效范围沿层级向上传播：子层脏像素矩形 -> 父层受影响像素矩形 -> 父层块。
 * - 滤波核可跨块取样；图像边缘采用镜像延拓（reflect101）。
 * - 更新按层级顺序执行；update(budget) 支持“未完成的一轮”。
 *   每个任务携带调度时的版本号，执行与提交时双重校验：
 *   过期任务/过期结果一律丢弃，绝不覆盖更新版本的父块。
 * - 父块任务执行前要求其采样足迹内的子块全部干净，否则推迟到队尾，
 *   保证父块不会用未更新的子块数据计算。
 */

export interface Kernel {
  /** 单轴权重，长度 = 2r + 2，已归一化（和为 1）。 */
  readonly taps: readonly number[];
  /** 核半径 r：父像素 px 采样子层 x ∈ [2*px - r, 2*px + r + 1]。 */
  readonly radius: number;
}

/** 由单轴权重构造核。权重个数必须为不小于 2 的偶数。 */
export function makeKernel(weights: readonly number[]): Kernel {
  if (weights.length < 2 || weights.length % 2 !== 0) {
    throw new Error("kernel 权重个数必须为不小于 2 的偶数");
  }
  const sum = weights.reduce((a, b) => a + b, 0);
  if (!(sum > 0)) throw new Error("kernel 权重和必须为正");
  const taps = weights.map((w) => w / sum);
  return { taps, radius: taps.length / 2 - 1 };
}

/** 默认核：二项式 [1,3,3,1]/8，半径 1，父像素足迹为子层 4x4。 */
export const DEFAULT_KERNEL: Kernel = makeKernel([1, 3, 3, 1]);

/** 镜像延拓（reflect101）：-1 -> 1，n -> n-2。 */
export function mirror(index: number, size: number): number {
  if (size <= 1) return 0;
  const period = 2 * size - 2;
  let m = index % period;
  if (m < 0) m += period;
  return m < size ? m : period - m;
}

export interface Block {
  readonly level: number;
  readonly bx: number;
  readonly by: number;
  /** 脏版本号：每次被标记为脏时递增。 */
  version: number;
  /** 当前 data 对应的版本号；cleanVersion === version 表示数据是最新的。 */
  cleanVersion: number;
  /** blockSize x blockSize，行主序。越界区域内容无意义，不会被读取。 */
  readonly data: Float32Array;
}

export interface Level {
  readonly index: number;
  readonly width: number;
  readonly height: number;
  readonly blocksX: number;
  readonly blocksY: number;
  /** 行主序块数组，长度 blocksX * blocksY。 */
  readonly blocks: Block[];
}

/** 一个待执行的块重算任务。version 为调度时刻的块版本。 */
export interface Task {
  readonly block: Block;
  readonly version: number;
}

export interface EditRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export type EditValue = number | ((x: number, y: number, current: number) => number);

export interface PyramidOptions {
  width: number;
  height: number;
  /** 块边长（像素），默认 16。 */
  blockSize?: number;
  /** 可分离核单轴权重，默认 [1,3,3,1]。 */
  kernel?: readonly number[];
  /** 第 0 层初始像素，默认全 0。 */
  initial?: (x: number, y: number) => number;
}

export class Pyramid {
  readonly width: number;
  readonly height: number;
  readonly blockSize: number;
  readonly kernel: Kernel;
  readonly levels: Level[];

  private readonly queue: Task[] = [];

  /** 块被实际重算并提交后触发（测试/监控用）。 */
  onRecompute?: (block: Block) => void;
  /**
   * 结果计算完成、即将提交前触发（测试/异步适配用）。
   * 若在此回调中发生新的编辑，本次结果将因版本校验失败被丢弃。
   */
  onBeforeCommit?: (block: Block, task: Task) => void;

  constructor(options: PyramidOptions) {
    const { width, height } = options;
    if (!(width >= 1) || !(height >= 1)) throw new Error("非法图像尺寸");
    this.width = width;
    this.height = height;
    this.blockSize = options.blockSize ?? 16;
    if (!(this.blockSize >= 1)) throw new Error("非法块大小");
    this.kernel = options.kernel ? makeKernel(options.kernel) : DEFAULT_KERNEL;

    // 建层：尺寸逐层减半（向上取整），直到 1x1。
    this.levels = [];
    let w = width;
    let h = height;
    for (let index = 0; ; index++) {
      const blocksX = Math.ceil(w / this.blockSize);
      const blocksY = Math.ceil(h / this.blockSize);
      const blocks: Block[] = [];
      for (let by = 0; by < blocksY; by++) {
        for (let bx = 0; bx < blocksX; bx++) {
          blocks.push({
            level: index,
            bx,
            by,
            version: 0,
            // 第 0 层数据由编辑直接写入，始终视为最新；上层块待计算。
            cleanVersion: index === 0 ? 0 : -1,
            data: new Float32Array(this.blockSize * this.blockSize),
          });
        }
      }
      this.levels.push({ index, width: w, height: h, blocksX, blocksY, blocks });
      if (w === 1 && h === 1) break;
      w = Math.ceil(w / 2);
      h = Math.ceil(h / 2);
    }

    // 写入第 0 层初始像素。
    const initial = options.initial ?? (() => 0);
    const base = this.levels[0];
    for (let y = 0; y < base.height; y++) {
      for (let x = 0; x < base.width; x++) {
        this.writeBasePixel(x, y, initial(x, y));
      }
    }

    // 初始构建等价于一次全图编辑：全部上层块进入待更新队列。
    this.invalidateFromBase({ x: 0, y: 0, width, height });
  }

  /** 待处理任务数（含已过期、执行时会被跳过的任务）。 */
  get pendingTasks(): number {
    return this.queue.length;
  }

  /** 是否所有层都已是最新。 */
  get isClean(): boolean {
    for (const level of this.levels) {
      for (const block of level.blocks) {
        if (block.cleanVersion !== block.version) return false;
      }
    }
    return true;
  }

  /** 当前待处理任务快照（不出队），供调度器/测试使用。 */
  peekTasks(): readonly Task[] {
    return this.queue.slice();
  }

  /**
   * 局部编辑第 0 层。只重写矩形内的像素，并精确失效受影响的祖先块。
   * 矩形自动裁剪到图像范围内；完全在图外时为空操作。
   */
  edit(rect: EditRect, value: EditValue): void {
    const x0 = Math.max(0, rect.x);
    const y0 = Math.max(0, rect.y);
    const x1 = Math.min(this.width - 1, rect.x + rect.width - 1);
    const y1 = Math.min(this.height - 1, rect.y + rect.height - 1);
    if (x0 > x1 || y0 > y1) return;

    for (let y = y0; y <= y1; y++) {
      for (let x = x0; x <= x1; x++) {
        const v = typeof value === "function" ? value(x, y, this.readPixel(0, x, y)) : value;
        this.writeBasePixel(x, y, v);
      }
    }
    this.invalidateFromBase({ x: x0, y: y0, width: x1 - x0 + 1, height: y1 - y0 + 1 });
  }

  /**
   * 执行队列中至多 budget 个有效任务（按层级从低到高的顺序）。
   * 过期任务被丢弃且不计入 budget。返回实际重算的块数。
   * budget 缺省为无限，即跑完一整轮。
   */
  update(budget = Number.POSITIVE_INFINITY): number {
    let done = 0;
    while (this.queue.length > 0 && done < budget) {
      const task = this.queue.shift()!;
      if (this.runTask(task, true)) done++;
    }
    return done;
  }

  /**
   * 执行单个任务（带版本校验）。返回是否真正提交了结果。
   * - 任务过期（块已被更新的编辑弄脏）或块已是最新：直接跳过。
   * - 采样足迹内的子块未全部干净：跳过；defer 为 true 时把任务重新排到队尾。
   * - 计算完成后、提交前再次校验版本：若期间发生新编辑，丢弃过期结果，
   *   保证旧结果绝不覆盖更新版本的父块。
   */
  runTask(task: Task, defer = false): boolean {
    const { block, version } = task;
    if (block.version !== version || block.cleanVersion === version) return false;
    if (!this.childrenClean(block)) {
      // 足迹内的子块尚未更新：推迟到队尾，等子块就绪后再算。
      if (defer) this.queue.push(task);
      return false;
    }
    const result = this.computeBlock(block);
    this.onBeforeCommit?.(block, task);
    if (block.version !== version) return false; // 计算期间出现更新的编辑，丢弃旧结果
    block.data.set(result);
    block.cleanVersion = version;
    this.onRecompute?.(block);
    return true;
  }

  /** 读取某层像素（坐标必须已在层范围内）。 */
  readPixel(level: number, x: number, y: number): number {
    const lvl = this.levels[level];
    const bs = this.blockSize;
    const block = lvl.blocks[Math.floor(y / bs) * lvl.blocksX + Math.floor(x / bs)];
    return block.data[(y % bs) * bs + (x % bs)];
  }

  /** 导出某层完整像素（行主序，width*height），供校验/调试。 */
  toArray(level: number): Float32Array {
    const lvl = this.levels[level];
    const out = new Float32Array(lvl.width * lvl.height);
    for (let y = 0; y < lvl.height; y++) {
      for (let x = 0; x < lvl.width; x++) {
        out[y * lvl.width + x] = this.readPixel(level, x, y);
      }
    }
    return out;
  }

  private writeBasePixel(x: number, y: number, value: number): void {
    const base = this.levels[0];
    const bs = this.blockSize;
    const block = base.blocks[Math.floor(y / bs) * base.blocksX + Math.floor(x / bs)];
    block.data[(y % bs) * bs + (x % bs)] = value;
  }

  /**
   * 由第 0 层脏矩形出发，逐级计算精确的受影响像素范围并标记脏块。
   * 父像素 px 的采样足迹为子层 [2px - r, 2px + r + 1]，
   * 故子层脏区间 [x0, x1] 影响父层 px ∈ [ceil((x0-r-1)/2), floor((x1+r)/2)]。
   */
  private invalidateFromBase(rect: EditRect): void {
    const r = this.kernel.radius;
    let x0 = rect.x;
    let y0 = rect.y;
    let x1 = rect.x + rect.width - 1;
    let y1 = rect.y + rect.height - 1;

    for (let L = 1; L < this.levels.length; L++) {
      const lvl = this.levels[L];
      const px0 = Math.max(0, Math.ceil((x0 - r - 1) / 2));
      const py0 = Math.max(0, Math.ceil((y0 - r - 1) / 2));
      const px1 = Math.min(lvl.width - 1, Math.floor((x1 + r) / 2));
      const py1 = Math.min(lvl.height - 1, Math.floor((y1 + r) / 2));
      if (px0 > px1 || py0 > py1) return;

      const bs = this.blockSize;
      for (let by = Math.floor(py0 / bs); by <= Math.floor(py1 / bs); by++) {
        for (let bx = Math.floor(px0 / bs); bx <= Math.floor(px1 / bs); bx++) {
          this.dirtyBlock(lvl.blocks[by * lvl.blocksX + bx]);
        }
      }
      x0 = px0;
      y0 = py0;
      x1 = px1;
      y1 = py1;
    }
  }

  private dirtyBlock(block: Block): void {
    block.version++;
    this.queue.push({ block, version: block.version });
  }

  /** 块的采样足迹覆盖到的所有子块是否都已干净（数据最新）。 */
  private childrenClean(block: Block): boolean {
    const lvl = this.levels[block.level];
    const child = this.levels[block.level - 1];
    const bs = this.blockSize;
    const r = this.kernel.radius;
    const x0 = block.bx * bs;
    const y0 = block.by * bs;
    const x1 = Math.min(x0 + bs, lvl.width) - 1;
    const y1 = Math.min(y0 + bs, lvl.height) - 1;
    const cx0 = Math.max(0, 2 * x0 - r);
    const cy0 = Math.max(0, 2 * y0 - r);
    const cx1 = Math.min(child.width - 1, 2 * x1 + r + 1);
    const cy1 = Math.min(child.height - 1, 2 * y1 + r + 1);
    for (let cby = Math.floor(cy0 / bs); cby <= Math.floor(cy1 / bs); cby++) {
      for (let cbx = Math.floor(cx0 / bs); cbx <= Math.floor(cx1 / bs); cbx++) {
        const cb = child.blocks[cby * child.blocksX + cbx];
        if (cb.cleanVersion !== cb.version) return false;
      }
    }
    return true;
  }

  /** 重算一个块：对块内每个父像素做可分离核降采样，跨块读取子层，边缘镜像。 */
  private computeBlock(block: Block): Float32Array {
    const lvl = this.levels[block.level];
    const child = this.levels[block.level - 1];
    const bs = this.blockSize;
    const { taps, radius } = this.kernel;
    const out = new Float32Array(bs * bs);

    const xStart = block.bx * bs;
    const yStart = block.by * bs;
    const xEnd = Math.min(xStart + bs, lvl.width);
    const yEnd = Math.min(yStart + bs, lvl.height);

    for (let py = yStart; py < yEnd; py++) {
      for (let px = xStart; px < xEnd; px++) {
        let sum = 0;
        for (let ky = 0; ky < taps.length; ky++) {
          const cy = mirror(2 * py + ky - radius, child.height);
          const wy = taps[ky];
          for (let kx = 0; kx < taps.length; kx++) {
            const cx = mirror(2 * px + kx - radius, child.width);
            sum += wy * taps[kx] * this.readPixel(block.level - 1, cx, cy);
          }
        }
        out[(py - yStart) * bs + (px - xStart)] = sum;
      }
    }
    return out;
  }
}
