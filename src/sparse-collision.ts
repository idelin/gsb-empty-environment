/**
 * 稀疏像素碰撞库。
 *
 * 每个对象维护一个稀疏像素集合（整数格点坐标）。索引基于
 * “像素 -> 拥有者集合”的倒排表，配合 “对象对 -> 交叠像素数” 的
 * 增量计数器：对象的平移 / 换形只会触及该对象实际增删的像素，
 * 绝不重扫全部对象对。
 *
 * 形状更新（setShape / translate）遵循“先校验、后变更”的两阶段
 * 协议：任何校验失败都会在索引被修改之前抛出，因此旧形状撤销与
 * 新形状插入对外表现为原子生效。
 */

export interface Pixel {
  x: number;
  y: number;
}

export interface CollisionPair {
  /** 字典序较小的对象 id */
  a: string;
  /** 字典序较大的对象 id */
  b: string;
  /** 交叠像素数 */
  overlap: number;
}

export class ShapeValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ShapeValidationError";
  }
}

export class ObjectNotFoundError extends Error {
  constructor(id: string) {
    super(`object not found: ${id}`);
    this.name = "ObjectNotFoundError";
  }
}

export class ObjectAlreadyExistsError extends Error {
  constructor(id: string) {
    super(`object already exists: ${id}`);
    this.name = "ObjectAlreadyExistsError";
  }
}

const PAIR_SEP = "\u0001";

function pairKey(a: string, b: string): string {
  return a < b ? a + PAIR_SEP + b : b + PAIR_SEP + a;
}

function pixelKey(x: number, y: number): string {
  return x + "," + y;
}

/** 校验并规范化形状，返回像素 key 列表。任何非法输入都会抛错。 */
function validatePixels(
  pixels: Iterable<Pixel>,
  opts: { checkDuplicates: boolean },
): string[] {
  const keys: string[] = [];
  const seen = opts.checkDuplicates ? new Set<string>() : null;
  let index = 0;
  for (const p of pixels) {
    if (p === null || typeof p !== "object") {
      throw new ShapeValidationError(`pixel #${index} is not an object`);
    }
    const { x, y } = p;
    if (!Number.isFinite(x) || !Number.isFinite(y)) {
      throw new ShapeValidationError(
        `pixel #${index} has non-finite coordinate: (${x}, ${y})`,
      );
    }
    if (!Number.isInteger(x) || !Number.isInteger(y)) {
      throw new ShapeValidationError(
        `pixel #${index} has non-integer coordinate: (${x}, ${y})`,
      );
    }
    if (x < 0 || y < 0) {
      throw new ShapeValidationError(
        `pixel #${index} has negative coordinate: (${x}, ${y})`,
      );
    }
    const key = pixelKey(x, y);
    if (seen) {
      if (seen.has(key)) {
        throw new ShapeValidationError(`duplicate pixel: (${x}, ${y})`);
      }
      seen.add(key);
    }
    keys.push(key);
    index++;
  }
  return keys;
}

export class SparseCollisionWorld {
  /** 对象 id -> 像素 key 集合 */
  private readonly objectPixels = new Map<string, Set<string>>();
  /** 像素 key -> 占据该像素的对象 id 集合（倒排索引） */
  private readonly pixelOwners = new Map<string, Set<string>>();
  /** 对象对 key -> 交叠像素数（只保留 > 0 的项） */
  private readonly pairOverlap = new Map<string, number>();

  get size(): number {
    return this.objectPixels.size;
  }

  hasObject(id: string): boolean {
    return this.objectPixels.has(id);
  }

  /** 返回对象的像素坐标快照。 */
  getPixels(id: string): Pixel[] {
    const keys = this.requireObject(id);
    const out: Pixel[] = [];
    for (const key of keys) {
      const sep = key.indexOf(",");
      out.push({
        x: Number(key.slice(0, sep)),
        y: Number(key.slice(sep + 1)),
      });
    }
    return out;
  }

  /** 新增对象。若 id 已存在或形状非法则抛错，世界状态不变。 */
  addObject(id: string, pixels: Iterable<Pixel>): void {
    if (this.objectPixels.has(id)) {
      throw new ObjectAlreadyExistsError(id);
    }
    const keys = validatePixels(pixels, { checkDuplicates: true });
    this.objectPixels.set(id, new Set(keys));
    for (const key of keys) {
      this.insertPixel(id, key);
    }
  }

  /** 移除对象及其全部碰撞记录。 */
  removeObject(id: string): void {
    const keys = this.requireObject(id);
    for (const key of keys) {
      this.removePixel(id, key);
    }
    this.objectPixels.delete(id);
  }

  /**
   * 原子换形：先完整校验新形状，校验通过后才撤销旧形状并插入新形状。
   * 校验失败时旧形状与全部碰撞计数保持不变。
   */
  setShape(id: string, pixels: Iterable<Pixel>): void {
    const oldKeys = this.requireObject(id);
    // 阶段一：仅校验，不触碰索引。
    const newKeys = validatePixels(pixels, { checkDuplicates: true });
    // 阶段二：提交变更。此阶段不会抛出。
    for (const key of oldKeys) {
      this.removePixel(id, key);
    }
    this.objectPixels.set(id, new Set(newKeys));
    for (const key of newKeys) {
      this.insertPixel(id, key);
    }
  }

  /**
   * 原子平移：等价于 setShape（整体偏移），但平移不会产生重复像素，
   * 因此跳过重复检测。负坐标等非法结果同样在校验阶段抛出。
   */
  translate(id: string, dx: number, dy: number): void {
    const oldKeys = this.requireObject(id);
    if (!Number.isInteger(dx) || !Number.isInteger(dy)) {
      throw new ShapeValidationError(
        `translation delta must be integers: (${dx}, ${dy})`,
      );
    }
    const newPixels: Pixel[] = [];
    for (const key of oldKeys) {
      const sep = key.indexOf(",");
      newPixels.push({
        x: Number(key.slice(0, sep)) + dx,
        y: Number(key.slice(sep + 1)) + dy,
      });
    }
    const newKeys = validatePixels(newPixels, { checkDuplicates: false });
    for (const key of oldKeys) {
      this.removePixel(id, key);
    }
    this.objectPixels.set(id, new Set(newKeys));
    for (const key of newKeys) {
      this.insertPixel(id, key);
    }
  }

  /** 查询两个对象的交叠像素数。 */
  getOverlap(a: string, b: string): number {
    if (a === b) return 0;
    return this.pairOverlap.get(pairKey(a, b)) ?? 0;
  }

  /** 当前全部碰撞对象对及交叠像素数，按 (a, b) 排序返回。 */
  getCollisions(): CollisionPair[] {
    const out: CollisionPair[] = [];
    for (const [key, overlap] of this.pairOverlap) {
      const sep = key.indexOf(PAIR_SEP);
      out.push({ a: key.slice(0, sep), b: key.slice(sep + 1), overlap });
    }
    out.sort((p, q) =>
      p.a < q.a ? -1 : p.a > q.a ? 1 : p.b < q.b ? -1 : p.b > q.b ? 1 : 0,
    );
    return out;
  }

  /** 与指定对象碰撞的全部对象对及交叠像素数。 */
  getCollisionsFor(id: string): CollisionPair[] {
    return this.getCollisions().filter((p) => p.a === id || p.b === id);
  }

  private requireObject(id: string): Set<string> {
    const keys = this.objectPixels.get(id);
    if (!keys) {
      throw new ObjectNotFoundError(id);
    }
    return keys;
  }

  /** 向倒排索引插入一个像素，并增量更新对象对计数。 */
  private insertPixel(id: string, key: string): void {
    let owners = this.pixelOwners.get(key);
    if (!owners) {
      owners = new Set();
      this.pixelOwners.set(key, owners);
    }
    for (const other of owners) {
      if (other === id) continue;
      const pk = pairKey(id, other);
      this.pairOverlap.set(pk, (this.pairOverlap.get(pk) ?? 0) + 1);
    }
    owners.add(id);
  }

  /** 从倒排索引移除一个像素，并增量更新对象对计数。 */
  private removePixel(id: string, key: string): void {
    const owners = this.pixelOwners.get(key);
    if (!owners) return;
    owners.delete(id);
    for (const other of owners) {
      const pk = pairKey(id, other);
      const count = (this.pairOverlap.get(pk) ?? 0) - 1;
      if (count > 0) {
        this.pairOverlap.set(pk, count);
      } else {
        this.pairOverlap.delete(pk);
      }
    }
    if (owners.size === 0) {
      this.pixelOwners.delete(key);
    }
  }
}
