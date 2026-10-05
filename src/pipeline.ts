/**
 * 复合变换管线。
 *
 * 一个管线由若干步骤组成，每步把上一坐标系映射到下一坐标系：
 *   原图坐标 --crop--> --scale--> --flip--> --rotate90--> 输出坐标
 *
 * 能力：
 * - 正向映射（原图 -> 输出）与反向映射（输出 -> 原图，即反查原图区域）；
 * - 裁剪分析：输出被裁掉的部分（clipped）与仍可恢复的部分（recoverable）；
 * - 逆序恢复：invert() 生成逆序逆变换管线，verifyRoundTrip() 验证恢复精度。
 *
 * 全程 float64，不做任何整数取整（取整仅在 rasterize.ts）。
 */

import type { Point, Rect } from "./geometry.ts";
import {
  intersectRect,
  isEmptyRect,
  rect,
  rectContainsPoint,
  rectFromSize,
  subtractRect,
} from "./geometry.ts";
import type { Affine } from "./matrix.ts";
import {
  IDENTITY,
  affineAlmostEqual,
  applyAffine,
  composeAffine,
  invertAffine,
  mapRectAffine,
} from "./matrix.ts";

export type StepName =
  | "crop"
  | "scale"
  | "flipX"
  | "flipY"
  | "rotate90cw"
  | "rotate90ccw"
  | "translate";

export interface Step {
  readonly name: StepName;
  /** 正向矩阵：本步输入坐标系 -> 输出坐标系 */
  readonly matrix: Affine;
  /** 逆向矩阵：matrix 的精确逆 */
  readonly inverse: Affine;
  /**
   * 裁剪窗口（本步输入坐标系下的半开矩形）。
   * 仅 crop 步骤携带；落在窗口外的数据在该步被丢弃，不可恢复。
   */
  readonly clip: Rect | null;
  /** 本步输入坐标系的边界 */
  readonly inputBounds: Rect;
  /** 本步输出坐标系的边界 */
  readonly outputBounds: Rect;
}

export interface ClipAnalysis {
  /** 仍可恢复部分：原图坐标系下的半开矩形；全部被裁掉时为 null */
  readonly recoverable: Rect | null;
  /** 被裁掉部分：原图坐标系下互不重叠的半开矩形列表（0~4 个） */
  readonly clipped: Rect[];
}

export interface RoundTripReport {
  readonly ok: boolean;
  /** 复合矩阵与其逆序逆矩阵复合后相对单位阵的最大绝对误差 */
  readonly maxError: number;
}

export class TransformPipeline {
  readonly steps: Step[] = [];
  /** 当前（末端）坐标系边界 */
  private currentBounds: Rect;
  /** 原图（首坐标系）边界 */
  readonly sourceBounds: Rect;

  private constructor(sourceBounds: Rect) {
    this.sourceBounds = sourceBounds;
    this.currentBounds = sourceBounds;
  }

  /** 以原图宽高（像素）创建空管线 */
  static forImage(width: number, height: number): TransformPipeline {
    if (!(width > 0) || !(height > 0)) {
      throw new RangeError(`非法图像尺寸：${width} x ${height}`);
    }
    return new TransformPipeline(rectFromSize(width, height));
  }

  /** 当前坐标系边界（无步骤时即原图边界） */
  get bounds(): Rect {
    return this.currentBounds;
  }

  private push(name: StepName, matrix: Affine, clip: Rect | null, outputBounds: Rect): this {
    if (isEmptyRect(outputBounds)) {
      throw new RangeError(`步骤 ${name} 产生空输出区域`);
    }
    this.steps.push({
      name,
      matrix,
      inverse: invertAffine(matrix),
      clip,
      inputBounds: this.currentBounds,
      outputBounds,
    });
    this.currentBounds = outputBounds;
    return this;
  }

  /**
   * 裁剪：保留当前坐标系中窗口 [x, x+width) × [y, y+height)，其余丢弃。
   * 窗口允许为分数坐标；允许超出当前边界（超出部分本就无数据）。
   */
  crop(x: number, y: number, width: number, height: number): this {
    if (!(width > 0) || !(height > 0)) {
      throw new RangeError(`非法裁剪窗口：${width} x ${height}`);
    }
    const matrix: Affine = { a: 1, b: 0, c: 0, d: 1, e: -x, f: -y };
    const clip = rect(x, y, x + width, y + height);
    return this.push("crop", matrix, clip, rectFromSize(width, height));
  }

  /** 缩放：x' = sx*x, y' = sy*y。系数必须非零；负系数等价于缩放+翻转。 */
  scale(sx: number, sy: number = sx): this {
    if (sx === 0 || sy === 0) {
      throw new RangeError("缩放系数不能为 0");
    }
    const matrix: Affine = { a: sx, b: 0, c: 0, d: sy, e: 0, f: 0 };
    return this.push("scale", matrix, null, mapRectAffine(matrix, this.currentBounds));
  }

  /** 水平翻转：x' = W - x（W 为当前坐标系宽度） */
  flipX(): this {
    const matrix: Affine = { a: -1, b: 0, c: 0, d: 1, e: this.currentBounds.x0 + this.currentBounds.x1, f: 0 };
    return this.push("flipX", matrix, null, this.currentBounds);
  }

  /** 垂直翻转：y' = H - y（H 为当前坐标系高度） */
  flipY(): this {
    const matrix: Affine = { a: 1, b: 0, c: 0, d: -1, e: 0, f: this.currentBounds.y0 + this.currentBounds.y1 };
    return this.push("flipY", matrix, null, this.currentBounds);
  }

  /** 顺时针旋转 90 度：(x, y) -> (H - y, x)，宽高互换 */
  rotate90CW(): this {
    const h = this.currentBounds.y1 - this.currentBounds.y0;
    const w = this.currentBounds.x1 - this.currentBounds.x0;
    const { x0, y0 } = this.currentBounds;
    // (x,y) -> (h - (y - y0), x - x0) = (y0 + h - y, x - x0)
    const matrix: Affine = { a: 0, b: 1, c: -1, d: 0, e: y0 + h, f: -x0 };
    return this.push("rotate90cw", matrix, null, rectFromSize(h, w));
  }

  /** 逆时针旋转 90 度：(x, y) -> (y, W - x)，宽高互换 */
  rotate90CCW(): this {
    const h = this.currentBounds.y1 - this.currentBounds.y0;
    const w = this.currentBounds.x1 - this.currentBounds.x0;
    const { x0, y0 } = this.currentBounds;
    // (x,y) -> (y - y0, w - (x - x0)) = (y - y0, x0 + w - x)
    const matrix: Affine = { a: 0, b: -1, c: 1, d: 0, e: -y0, f: x0 + w };
    return this.push("rotate90ccw", matrix, null, rectFromSize(h, w));
  }

  /** 平移（不裁剪） */
  translate(tx: number, ty: number): this {
    const matrix: Affine = { a: 1, b: 0, c: 0, d: 1, e: tx, f: ty };
    return this.push("translate", matrix, null, mapRectAffine(matrix, this.currentBounds));
  }

  // ---------------------------------------------------------------- 正向映射

  /**
   * 点正向映射。途经裁剪窗口时按闭区间判断包含；
   * 被裁掉的点返回 null（不可恢复）。
   */
  mapPointForward(p: Point): Point | null {
    let cur = p;
    for (const step of this.steps) {
      if (step.clip !== null && !rectContainsPoint(step.clip, cur)) return null;
      cur = applyAffine(step.matrix, cur);
    }
    return cur;
  }

  /**
   * 矩形正向映射（半开区间）。途经裁剪窗口时求交；
   * 被完全裁掉时返回 null。
   */
  mapRectForward(r: Rect): Rect | null {
    let cur = r;
    for (const step of this.steps) {
      if (step.clip !== null) {
        const hit = intersectRect(cur, step.clip);
        if (hit === null) return null;
        cur = hit;
      }
      cur = mapRectAffine(step.matrix, cur);
    }
    return cur;
  }

  // ---------------------------------------------------------------- 反向映射

  /**
   * 点反向映射（输出坐标 -> 原图坐标）。
   * 途经裁剪窗口时按闭区间判断；落在被裁区域的点返回 null。
   */
  mapPointBackward(p: Point): Point | null {
    let cur = p;
    for (let i = this.steps.length - 1; i >= 0; i--) {
      const step = this.steps[i]!;
      cur = applyAffine(step.inverse, cur);
      if (step.clip !== null && !rectContainsPoint(step.clip, cur)) return null;
    }
    return cur;
  }

  /**
   * 矩形反向映射（输出坐标 -> 原图坐标，半开区间）。
   * 途经裁剪窗口时求交；完全落在被裁区域时返回 null。
   */
  mapRectBackward(r: Rect): Rect | null {
    let cur = r;
    for (let i = this.steps.length - 1; i >= 0; i--) {
      const step = this.steps[i]!;
      cur = mapRectAffine(step.inverse, cur);
      if (step.clip !== null) {
        const hit = intersectRect(cur, step.clip);
        if (hit === null) return null;
        cur = hit;
      }
    }
    return cur;
  }

  // ---------------------------------------------------------------- 裁剪分析

  /**
   * 分析整个原图：哪些区域仍可恢复（在输出中可见），哪些已被裁掉。
   */
  analyze(): ClipAnalysis {
    return this.analyzeRect(this.sourceBounds);
  }

  /**
   * 分析原图中的任意矩形：返回其可恢复部分与被裁掉部分。
   */
  analyzeRect(r: Rect): ClipAnalysis {
    // 可恢复区域 = 输出边界经逆序逆变换、沿途与裁剪窗口求交后的原图区域
    const recoverableRegion = this.mapRectBackward(this.bounds);
    if (recoverableRegion === null) {
      return { recoverable: null, clipped: [r] };
    }
    const recoverable = intersectRect(r, recoverableRegion);
    if (recoverable === null) {
      return { recoverable: null, clipped: [r] };
    }
    return { recoverable, clipped: subtractRect(r, recoverable) };
  }

  // ---------------------------------------------------------------- 逆序恢复

  /**
   * 生成逆序逆变换管线：把输出坐标映射回原图坐标。
   * 步骤顺序反转，每步取逆；裁剪窗口保留（用于判断点/区域是否落在已丢弃部分）。
   */
  invert(): TransformPipeline {
    const inv = new TransformPipeline(this.bounds);
    for (let i = this.steps.length - 1; i >= 0; i--) {
      const step = this.steps[i]!;
      inv.pushInverseOf(step);
    }
    return inv;
  }

  private pushInverseOf(step: Step): void {
    // 逆步骤的裁剪窗口：crop 的逆是平移，本身不裁剪；
    // 但正向时窗口外数据已丢失，因此逆步骤携带同一窗口（位于其输出坐标系，
    // 即原步骤的输入坐标系），供反查时判断“该区域曾被裁掉”。
    const clipInOutput = step.clip;
    this.steps.push({
      name: step.name,
      matrix: step.inverse,
      inverse: step.matrix,
      clip: null,
      inputBounds: step.outputBounds,
      outputBounds: step.inputBounds,
    });
    this.currentBounds = step.inputBounds;
    if (clipInOutput !== null) {
      // 将窗口记录为逆管线最后一步的裁剪（作用于其输出侧），
      // 通过额外一步恒等+裁剪表达，保持“裁剪只发生在窗口坐标系”的不变量。
      this.steps.push({
        name: "crop",
        matrix: IDENTITY,
        inverse: IDENTITY,
        clip: clipInOutput,
        inputBounds: this.currentBounds,
        outputBounds: this.currentBounds,
      });
    }
  }

  /**
   * 验证逆序恢复：复合全部正向矩阵得 F，逆序复合全部逆向矩阵得 B，
   * 检查 B∘F 与 F∘B 是否为单位阵（容差 tol）。
   */
  verifyRoundTrip(tol = 1e-9): RoundTripReport {
    let forward = IDENTITY;
    for (const step of this.steps) {
      forward = composeAffine(step.matrix, forward);
    }
    let backward = IDENTITY;
    for (let i = this.steps.length - 1; i >= 0; i--) {
      backward = composeAffine(this.steps[i]!.inverse, backward);
    }
    const bf = composeAffine(backward, forward);
    const fb = composeAffine(forward, backward);
    const err = (m: Affine) =>
      Math.max(
        Math.abs(m.a - 1),
        Math.abs(m.b),
        Math.abs(m.c),
        Math.abs(m.d - 1),
        Math.abs(m.e),
        Math.abs(m.f),
      );
    const maxError = Math.max(err(bf), err(fb));
    return { ok: affineAlmostEqual(bf, IDENTITY, tol) && affineAlmostEqual(fb, IDENTITY, tol), maxError };
  }
}
