export interface Pixel {
  readonly x: number;
  readonly y: number;
}

export type ShapeInput = Iterable<Pixel>;

export interface CollisionPair {
  readonly a: string;
  readonly b: string;
  readonly overlap: number;
}

export type ObjectUpdate =
  | { readonly id: string; readonly kind: "set"; readonly pixels: ShapeInput }
  | { readonly id: string; readonly kind: "translate"; readonly dx: number; readonly dy: number }
  | { readonly id: string; readonly kind: "remove" };

export type CollisionErrorCode =
  | "INVALID_COORDINATE"
  | "NEGATIVE_COORDINATE"
  | "DUPLICATE_PIXEL"
  | "DUPLICATE_OBJECT"
  | "UNKNOWN_OBJECT"
  | "DUPLICATE_UPDATE";

export class CollisionError extends Error {
  readonly code: CollisionErrorCode;

  constructor(code: CollisionErrorCode, message: string) {
    super(message);
    this.name = "CollisionError";
    this.code = code;
  }
}

export interface WorldStats {
  readonly objects: number;
  readonly occupiedPixels: number;
  readonly collidingPairs: number;
  /** Total pair-counter adjustments since creation; a work probe for incrementality. */
  readonly pairAdjustments: number;
}

interface ShapePlan {
  readonly id: string;
  readonly next: Set<string> | null;
}

const keyOf = (x: number, y: number): string => `${x},${y}`;

function parseKey(key: string): Pixel {
  const comma = key.indexOf(",");
  return { x: Number(key.slice(0, comma)), y: Number(key.slice(comma + 1)) };
}

function assertObjectId(id: string): void {
  if (typeof id !== "string" || id.length === 0) {
    throw new CollisionError("UNKNOWN_OBJECT", `object id must be a non-empty string, got ${String(id)}`);
  }
}

/**
 * Sparse pixel collision world.
 *
 * Internally keeps an inverted index from pixel to its occupying objects and a
 * pair-overlap table derived from it. Updates only touch the pixels that
 * actually changed, so a translate/reshape costs O(changed pixels x occupants
 * per pixel) instead of a rescan of all object pairs. Queries read the
 * maintained pair table directly.
 */
export class SparsePixelCollisionWorld {
  private readonly shapes = new Map<string, Set<string>>();
  private readonly index = new Map<string, Set<string>>();
  private readonly pairs = new Map<string, Map<string, number>>();
  private adjustmentCount = 0;

  get objectCount(): number {
    return this.shapes.size;
  }

  get pairCount(): number {
    let total = 0;
    for (const inner of this.pairs.values()) total += inner.size;
    return total;
  }

  get stats(): WorldStats {
    return {
      objects: this.shapes.size,
      occupiedPixels: this.index.size,
      collidingPairs: this.pairCount,
      pairAdjustments: this.adjustmentCount,
    };
  }

  hasObject(id: string): boolean {
    return this.shapes.has(id);
  }

  getPixels(id: string): Pixel[] {
    const shape = this.mustGetShape(id);
    const pixels: Pixel[] = [];
    for (const key of shape) pixels.push(parseKey(key));
    pixels.sort((p, q) => p.x - q.x || p.y - q.y);
    return pixels;
  }

  addObject(id: string, pixels: ShapeInput): void {
    assertObjectId(id);
    if (this.shapes.has(id)) {
      throw new CollisionError("DUPLICATE_OBJECT", `object "${id}" already exists`);
    }
    const shape = this.normalizeShape(id, pixels);
    this.shapes.set(id, shape);
    this.attach(id, shape);
  }

  removeObject(id: string): void {
    const shape = this.mustGetShape(id);
    this.detach(id, shape);
    this.shapes.delete(id);
  }

  /** Atomically replaces the object's shape (or creates it if absent). */
  setShape(id: string, pixels: ShapeInput): void {
    assertObjectId(id);
    const shape = this.normalizeShape(id, pixels);
    const old = this.shapes.get(id);
    if (old !== undefined) this.detach(id, old);
    this.shapes.set(id, shape);
    this.attach(id, shape);
  }

  /** Atomically shifts every pixel of the object by (dx, dy). */
  translate(id: string, dx: number, dy: number): void {
    const next = this.planTranslate(id, dx, dy);
    const old = this.mustGetShape(id);
    this.detach(id, old);
    this.shapes.set(id, next);
    this.attach(id, next);
  }

  /**
   * Applies several updates as one atomic step: every update is validated and
   * planned before any mutation happens, so a failing update leaves the world
   * untouched. This also makes mutual position swaps safe — no transient
   * intermediate state is ever observable or indexed.
   */
  applyUpdates(updates: readonly ObjectUpdate[]): void {
    const seen = new Set<string>();
    const plans: ShapePlan[] = [];
    for (const update of updates) {
      assertObjectId(update.id);
      if (seen.has(update.id)) {
        throw new CollisionError("DUPLICATE_UPDATE", `batch contains two updates for object "${update.id}"`);
      }
      seen.add(update.id);
      switch (update.kind) {
        case "set":
          plans.push({ id: update.id, next: this.normalizeShape(update.id, update.pixels) });
          break;
        case "translate":
          plans.push({ id: update.id, next: this.planTranslate(update.id, update.dx, update.dy) });
          break;
        case "remove":
          this.mustGetShape(update.id);
          plans.push({ id: update.id, next: null });
          break;
      }
    }
    for (const plan of plans) {
      const old = this.shapes.get(plan.id);
      if (old !== undefined) this.detach(plan.id, old);
      if (plan.next === null) {
        this.shapes.delete(plan.id);
      } else {
        this.shapes.set(plan.id, plan.next);
        this.attach(plan.id, plan.next);
      }
    }
  }

  getOverlap(a: string, b: string): number {
    if (a === b) return 0;
    const [lo, hi] = a < b ? [a, b] : [b, a];
    return this.pairs.get(lo)?.get(hi) ?? 0;
  }

  /** All currently colliding pairs with their overlap pixel counts, sorted by id. */
  getCollidingPairs(): CollisionPair[] {
    const result: CollisionPair[] = [];
    for (const [lo, inner] of this.pairs) {
      for (const [hi, overlap] of inner) result.push({ a: lo, b: hi, overlap });
    }
    result.sort((p, q) => (p.a < q.a ? -1 : p.a > q.a ? 1 : p.b < q.b ? -1 : p.b > q.b ? 1 : 0));
    return result;
  }

  /** Every pair involving the given object. */
  getCollisions(id: string): CollisionPair[] {
    this.mustGetShape(id);
    return this.getCollidingPairs().filter((pair) => pair.a === id || pair.b === id);
  }

  private mustGetShape(id: string): Set<string> {
    assertObjectId(id);
    const shape = this.shapes.get(id);
    if (shape === undefined) {
      throw new CollisionError("UNKNOWN_OBJECT", `object "${id}" does not exist`);
    }
    return shape;
  }

  private normalizeShape(id: string, pixels: ShapeInput): Set<string> {
    const keys = new Set<string>();
    for (const pixel of pixels) {
      const { x, y } = pixel;
      if (!Number.isSafeInteger(x) || !Number.isSafeInteger(y)) {
        throw new CollisionError(
          "INVALID_COORDINATE",
          `object "${id}" has a non-integer coordinate (${String(x)}, ${String(y)})`,
        );
      }
      if (x < 0 || y < 0) {
        throw new CollisionError("NEGATIVE_COORDINATE", `object "${id}" has a negative coordinate (${x}, ${y})`);
      }
      const key = keyOf(x, y);
      if (keys.has(key)) {
        throw new CollisionError("DUPLICATE_PIXEL", `object "${id}" contains pixel (${x}, ${y}) twice`);
      }
      keys.add(key);
    }
    return keys;
  }

  private planTranslate(id: string, dx: number, dy: number): Set<string> {
    if (!Number.isSafeInteger(dx) || !Number.isSafeInteger(dy)) {
      throw new CollisionError("INVALID_COORDINATE", `translation (${String(dx)}, ${String(dy)}) must be integers`);
    }
    const old = this.mustGetShape(id);
    const next = new Set<string>();
    for (const key of old) {
      const { x, y } = parseKey(key);
      const nx = x + dx;
      const ny = y + dy;
      if (nx < 0 || ny < 0) {
        throw new CollisionError(
          "NEGATIVE_COORDINATE",
          `translating object "${id}" by (${dx}, ${dy}) moves pixel (${x}, ${y}) to a negative coordinate`,
        );
      }
      next.add(keyOf(nx, ny));
    }
    return next;
  }

  private attach(id: string, shape: Set<string>): void {
    for (const key of shape) {
      let occupants = this.index.get(key);
      if (occupants === undefined) {
        occupants = new Set<string>();
        this.index.set(key, occupants);
      }
      for (const other of occupants) this.bumpPair(id, other, 1);
      occupants.add(id);
    }
  }

  private detach(id: string, shape: Set<string>): void {
    for (const key of shape) {
      const occupants = this.index.get(key);
      if (occupants === undefined) continue;
      for (const other of occupants) {
        if (other !== id) this.bumpPair(id, other, -1);
      }
      occupants.delete(id);
      if (occupants.size === 0) this.index.delete(key);
    }
  }

  private bumpPair(a: string, b: string, delta: 1 | -1): void {
    this.adjustmentCount += 1;
    const [lo, hi] = a < b ? [a, b] : [b, a];
    if (delta === 1) {
      let inner = this.pairs.get(lo);
      if (inner === undefined) {
        inner = new Map<string, number>();
        this.pairs.set(lo, inner);
      }
      inner.set(hi, (inner.get(hi) ?? 0) + 1);
      return;
    }
    const inner = this.pairs.get(lo);
    const current = inner?.get(hi);
    if (inner === undefined || current === undefined) return;
    if (current <= 1) {
      inner.delete(hi);
      if (inner.size === 0) this.pairs.delete(lo);
    } else {
      inner.set(hi, current - 1);
    }
  }
}
