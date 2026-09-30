export interface Vec { x: number; y: number; }

/** 2D affine transform: [a c e; b d f]. */
export interface Affine { a: number; b: number; c: number; d: number; e: number; f: number; }

export const IDENTITY: Affine = { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };

export function applyAffine(t: Affine, p: Vec): Vec {
  return { x: t.a * p.x + t.c * p.y + t.e, y: t.b * p.x + t.d * p.y + t.f };
}

export function composeAffine(m: Affine, n: Affine): Affine {
  // m ∘ n  (apply n first, then m)
  return {
    a: m.a * n.a + m.c * n.b,
    b: m.b * n.a + m.d * n.b,
    c: m.a * n.c + m.c * n.d,
    d: m.b * n.c + m.d * n.d,
    e: m.a * n.e + m.c * n.f + m.e,
    f: m.b * n.e + m.d * n.f + m.f,
  };
}

export function invertAffine(t: Affine): Affine | null {
  const det = t.a * t.d - t.b * t.c;
  if (det === 0) return null;
  const ia = t.d / det, ib = -t.b / det, ic = -t.c / det, id = t.a / det;
  return { a: ia, b: ib, c: ic, d: id, e: -(ia * t.e + ic * t.f), f: -(ib * t.e + id * t.f) };
}

export function dist(p: Vec, q: Vec): number {
  return Math.hypot(p.x - q.x, p.y - q.y);
}

/**
 * Converts points between named coordinate spaces. Transforms are registered
 * relative to a canonical root space; conversion goes source -> root -> target.
 * Unknown space pairs yield null (the arbiter treats that as a hard re-anchor).
 */
export class SpaceConverter {
  private toRoot = new Map<string, Affine>();
  private rootSpace: string;

  constructor(rootSpace: string) {
    this.rootSpace = rootSpace;
    this.toRoot.set(rootSpace, IDENTITY);
  }

  register(space: string, toRoot: Affine): void {
    this.toRoot.set(space, toRoot);
  }

  has(space: string): boolean {
    return this.toRoot.has(space);
  }

  convert(p: Vec, from: string, to: string): Vec | null {
    const srcToRoot = this.toRoot.get(from);
    const dstToRoot = this.toRoot.get(to);
    if (!srcToRoot || !dstToRoot) return null;
    const rootToDst = invertAffine(dstToRoot);
    if (!rootToDst) return null;
    return applyAffine(composeAffine(rootToDst, srcToRoot), p);
  }

  toCanonical(p: Vec, from: string): Vec | null {
    return this.convert(p, from, this.rootSpace);
  }
}
