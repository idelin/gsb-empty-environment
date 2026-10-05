/**
 * 标注坐标变换流水线：裁剪、缩放、翻转、90°旋转的复合，
 * 支持从结果坐标反查原图区域，并输出被裁掉 / 仍可恢复的原图部分。
 *
 * 约定：
 * - 坐标为像素边界坐标，矩形为半开区间 [x0, x1) × [y0, y1)。
 * - 点（Vec2）与矩形（Rect）语义不同：点是连续位置；矩形是边界围成的区域，
 *   翻转/旋转时矩形表现为「边界互换」（见 mapRect）。
 * - 任何中间步骤都不取整；取整只发生在 raster.ts 的栅格化中。
 */
import {
  type Affine,
  IDENTITY,
  compose,
  invert,
  scaling,
  translation,
} from "./affine.ts";
import {
  type Rect,
  type Vec2,
  intersectRect,
  isEmptyRect,
  makeRect,
  subtractRect,
} from "./geometry.ts";

export type OpKind = "crop" | "scale" | "flipH" | "flipV" | "rotate90";

/** 流水线中的一步：记录正反矩阵与画布尺寸，便于逐步逆序回放。 */
export interface Step {
  readonly op: OpKind;
  /** 该步的正向矩阵（操作前坐标 → 操作后坐标）。 */
  readonly matrix: Affine;
  /** 该步的逆矩阵（操作后坐标 → 操作前坐标）。 */
  readonly inverse: Affine;
  /** 操作前画布尺寸（浮点，可能因缩放带小数）。 */
  readonly canvasBefore: { readonly width: number; readonly height: number };
  /** 操作后画布尺寸。 */
  readonly canvasAfter: { readonly width: number; readonly height: number };
  /** 该步完成后，当前画布内容对应原图中的可见区域（半开矩形）。 */
  readonly visibleInOriginal: Rect;
  /** 该步新裁掉的原图区域（0~4 个半开矩形；非裁剪操作为空数组）。 */
  readonly lostInOriginal: readonly Rect[];
}

export class TransformPipeline {
  readonly originalWidth: number;
  readonly originalHeight: number;

  #width: number;
  #height: number;
  /** 原图坐标 → 当前坐标 的复合矩阵。 */
  #fwd: Affine = IDENTITY;
  /** 当前坐标 → 原图坐标 的复合矩阵（逐步逆序复合而成）。 */
  #inv: Affine = IDENTITY;
  #steps: Step[] = [];
  #visible: Rect;

  constructor(width: number, height: number) {
    if (!(width > 0) || !(height > 0)) {
      throw new Error("image size must be positive");
    }
    this.originalWidth = width;
    this.originalHeight = height;
    this.#width = width;
    this.#height = height;
    this.#visible = makeRect(0, 0, width, height);
  }

  /** 当前画布尺寸（浮点；缩放后可能不是整数，取整由栅格化决定）。 */
  get width(): number {
    return this.#width;
  }
  get height(): number {
    return this.#height;
  }
  get steps(): readonly Step[] {
    return this.#steps;
  }

  // ---------- 复合操作 ----------

  /**
   * 裁剪到当前坐标系下的半开矩形 r。
   * r 会先与当前画布求交；完全落在画布外则抛错。
   * 这是唯一会丢失信息的操作，丢失部分记录在 step.lostInOriginal。
   */
  crop(r: Rect): this {
    const effective = intersectRect(r, makeRect(0, 0, this.#width, this.#height));
    if (effective === null) throw new Error("crop rect is fully outside canvas");
    const m = translation(-effective.x0, -effective.y0);
    this.#push("crop", m, effective.x1 - effective.x0, effective.y1 - effective.y0);
    return this;
  }

  /** 缩放。因子必须为正；画布尺寸同步缩放（保持浮点，不取整）。 */
  scale(sx: number, sy: number = sx): this {
    if (!(sx > 0) || !(sy > 0)) throw new Error("scale factors must be positive");
    const m = scaling(sx, sy);
    this.#push("scale", m, this.#width * sx, this.#height * sy);
    return this;
  }

  /** 水平翻转：x' = W - x。点 0 与 W 互换；矩形 [x0,x1) → [W-x1, W-x0)。 */
  flipH(): this {
    const m: Affine = { a: -1, b: 0, c: 0, d: 1, tx: this.#width, ty: 0 };
    this.#push("flipH", m, this.#width, this.#height);
    return this;
  }

  /** 垂直翻转：y' = H - y。 */
  flipV(): this {
    const m: Affine = { a: 1, b: 0, c: 0, d: -1, tx: 0, ty: this.#height };
    this.#push("flipV", m, this.#width, this.#height);
    return this;
  }

  /**
   * 顺时针旋转 turns 个 90°（负数表示逆时针）。
   * turns=1: (x, y) → (H - y, x)，画布 (W, H) → (H, W)。
   */
  rotate90(turns = 1): this {
    const k = ((turns % 4) + 4) % 4;
    const w = this.#width;
    const h = this.#height;
    let m: Affine;
    let nw: number;
    let nh: number;
    switch (k) {
      case 1:
        m = { a: 0, b: -1, c: 1, d: 0, tx: h, ty: 0 };
        nw = h; nh = w;
        break;
      case 2:
        m = { a: -1, b: 0, c: 0, d: -1, tx: w, ty: h };
        nw = w; nh = h;
        break;
      case 3:
        m = { a: 0, b: 1, c: -1, d: 0, tx: 0, ty: w };
        nw = h; nh = w;
        break;
      default:
        return this; // k=0：无操作，不记录步骤
    }
    this.#push("rotate90", m, nw, nh);
    return this;
  }

  // ---------- 坐标映射 ----------

  /** 正向映射点：原图坐标 → 当前坐标。 */
  mapPoint(p: Vec2): Vec2 {
    return applyPoint(this.#fwd, p);
  }

  /** 反查点：当前坐标 → 原图坐标。 */
  unmapPoint(p: Vec2): Vec2 {
    return applyPoint(this.#inv, p);
  }

  /**
   * 正向映射半开矩形（边界语义）：映射边界后再取 min/max 规范化，
   * 因此翻转/旋转下自动完成「边界互换」，结果仍是半开区间。
   */
  mapRect(r: Rect): Rect {
    return applyRect(this.#fwd, r);
  }

  /** 反查半开矩形：当前坐标系下的区域 → 原图中的区域。 */
  unmapRect(r: Rect): Rect {
    return applyRect(this.#inv, r);
  }

  // ---------- 可恢复性分析 ----------

  /** 当前画布内容在原图中仍可恢复的区域（半开矩形，已裁剪到原图范围内）。 */
  recoverableRegion(): Rect {
    return this.#visible;
  }

  /** 被裁掉、无法从当前画布恢复的原图区域（0~4 个互不重叠的半开矩形）。 */
  clippedRegions(): Rect[] {
    return subtractRect(
      makeRect(0, 0, this.originalWidth, this.originalHeight),
      this.#visible,
    );
  }

  /**
   * 逆序恢复：把记录的所有步骤的逆矩阵按相反顺序复合，
   * 得到「当前坐标 → 原图坐标」的矩阵。结果与内部维护的 #inv 一致，
   * 供调用方验证变换的逆序恢复正确性。
   */
  restoreMatrix(): Affine {
    let m: Affine = IDENTITY;
    for (let i = this.#steps.length - 1; i >= 0; i--) {
      m = compose(this.#steps[i].inverse, m);
    }
    return m;
  }

  // ---------- 内部 ----------

  #push(op: OpKind, matrix: Affine, newWidth: number, newHeight: number): void {
    const before = { width: this.#width, height: this.#height };
    const inverse = invert(matrix);
    this.#fwd = compose(matrix, this.#fwd);
    this.#inv = compose(this.#inv, inverse);
    this.#width = newWidth;
    this.#height = newHeight;

    const prevVisible = this.#visible;
    const mapped = applyRect(this.#inv, makeRect(0, 0, newWidth, newHeight));
    const visible = intersectRect(
      mapped,
      makeRect(0, 0, this.originalWidth, this.originalHeight),
    );
    this.#visible = visible ?? makeRect(0, 0, 0, 0);
    const lost = subtractRect(prevVisible, this.#visible);

    this.#steps.push({
      op,
      matrix,
      inverse,
      canvasBefore: before,
      canvasAfter: { width: newWidth, height: newHeight },
      visibleInOriginal: this.#visible,
      lostInOriginal: lost,
    });
  }
}

/** 点映射：直接代入仿射式。 */
export function applyPoint(m: Affine, p: Vec2): Vec2 {
  return {
    x: m.a * p.x + m.b * p.y + m.tx,
    y: m.c * p.x + m.d * p.y + m.ty,
  };
}

/** 半开矩形映射：四角映射后取包围盒（轴对齐仿射下结果精确）。 */
export function applyRect(m: Affine, r: Rect): Rect {
  const c1 = applyPoint(m, { x: r.x0, y: r.y0 });
  const c2 = applyPoint(m, { x: r.x1, y: r.y0 });
  const c3 = applyPoint(m, { x: r.x0, y: r.y1 });
  const c4 = applyPoint(m, { x: r.x1, y: r.y1 });
  return makeRect(
    Math.min(c1.x, c2.x, c3.x, c4.x),
    Math.min(c1.y, c2.y, c3.y, c4.y),
    Math.max(c1.x, c2.x, c3.x, c4.x),
    Math.max(c1.y, c2.y, c3.y, c4.y),
  );
}
