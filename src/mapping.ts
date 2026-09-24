/**
 * Composable, invertible offset mappings between document versions.
 *
 * A Mapping is a structural object: an ordered list of edit steps
 * (UTF-16 code units). Composition concatenates step lists and inversion
 * reverses them, so a mapping never collapses to "just the final offsets" —
 * every intermediate edit remains inspectable and reversible.
 */

/** Stickiness of a zero-width position relative to adjacent text. */
export type Affinity = 'before' | 'after';

/** One atomic edit, in UTF-16 code units of the source version. */
export interface EditStep {
  /** UTF-16 offset where the edit begins. */
  readonly start: number;
  /** Number of UTF-16 code units deleted at `start`. */
  readonly deletedLen: number;
  /** Number of UTF-16 code units inserted at `start`. */
  readonly insertedLen: number;
}

/** A range (in the queried old version) that information was lost from / has no source in. */
export interface AmbiguitySource {
  readonly kind: 'deleted' | 'inserted';
  readonly start: number;
  readonly end: number;
}

export interface BackwardMapResult {
  readonly offset: number;
  /** True when the exact origin cannot be determined (info lost forward, or no origin). */
  readonly ambiguous: boolean;
  /** Why it is ambiguous: ranges that collapsed onto this point, or foreign inserted text. */
  readonly sources: readonly AmbiguitySource[];
}

function mapThroughStep(offset: number, step: EditStep, affinity: Affinity): number {
  if (offset < step.start) return offset;
  if (offset > step.start + step.deletedLen) {
    return offset + (step.insertedLen - step.deletedLen);
  }
  // Inside the replaced span (inclusive): collapse to a boundary by affinity.
  return affinity === 'after' ? step.start + step.insertedLen : step.start;
}

interface BackPoint {
  offset: number;
  source: AmbiguitySource | null;
}

/** Map one offset backwards through a single forward step. */
function backThroughStep(offset: number, step: EditStep, affinity: Affinity): BackPoint {
  const insertedEnd = step.start + step.insertedLen;
  if (offset < step.start) return { offset, source: null };
  if (offset > insertedEnd) {
    return { offset: offset - step.insertedLen + step.deletedLen, source: null };
  }
  const mapped = affinity === 'after' ? step.start + step.deletedLen : step.start;
  if (step.deletedLen > 0) {
    // Forward, the whole deleted span collapsed here: the exact origin is lost.
    return {
      offset: mapped,
      source: { kind: 'deleted', start: step.start, end: step.start + step.deletedLen },
    };
  }
  if (offset > step.start && offset < insertedEnd) {
    // Strictly inside text that only exists in the newer version: no origin at all.
    return { offset: mapped, source: { kind: 'inserted', start: step.start, end: insertedEnd } };
  }
  return { offset: mapped, source: null };
}

/** Map a point backwards through a step without ambiguity tracking (for source ranges). */
function backPoint(offset: number, step: EditStep, affinity: Affinity): number {
  return backThroughStep(offset, step, affinity).offset;
}

export class Mapping {
  /** Edit steps, applied in order from the oldest to the newest version. */
  readonly steps: readonly EditStep[];

  private constructor(steps: readonly EditStep[]) {
    this.steps = steps;
  }

  static identity(): Mapping {
    return new Mapping([]);
  }

  static fromEdit(step: EditStep): Mapping {
    return new Mapping([step]);
  }

  static fromEdits(steps: readonly EditStep[]): Mapping {
    return new Mapping(steps.slice());
  }

  /** Compose mappings that apply in sequence: compose(a, b) maps a.from -> b.to. */
  static compose(...mappings: readonly Mapping[]): Mapping {
    const steps: EditStep[] = [];
    for (const mapping of mappings) steps.push(...mapping.steps);
    return new Mapping(steps);
  }

  /** `this.compose(next)` maps from this mapping's source version to `next`'s target. */
  compose(next: Mapping): Mapping {
    return Mapping.compose(this, next);
  }

  /** Structural inverse: reversed step list with deleted/inserted swapped. */
  invert(): Mapping {
    const steps: EditStep[] = [];
    for (let i = this.steps.length - 1; i >= 0; i--) {
      const s = this.steps[i];
      steps.push({ start: s.start, deletedLen: s.insertedLen, insertedLen: s.deletedLen });
    }
    return new Mapping(steps);
  }

  get isIdentity(): boolean {
    return this.steps.every((s) => s.deletedLen === 0 && s.insertedLen === 0);
  }

  /** Map an offset from the source version to the target version. */
  mapOffset(offset: number, affinity: Affinity = 'before'): number {
    let o = offset;
    for (const step of this.steps) o = mapThroughStep(o, step, affinity);
    return o;
  }

  /**
   * Map an offset from the target version back to the source version.
   * Reports ambiguity when forward mapping destroyed the exact origin.
   */
  mapOffsetBack(offset: number, affinity: Affinity = 'before'): BackwardMapResult {
    let o = offset;
    let sources: AmbiguitySource[] = [];
    for (let i = this.steps.length - 1; i >= 0; i--) {
      const step = this.steps[i];
      // Carry already-collected source ranges further back.
      sources = sources.map((src) => ({
        kind: src.kind,
        start: backPoint(src.start, step, 'before'),
        end: backPoint(src.end, step, 'after'),
      }));
      const r = backThroughStep(o, step, affinity);
      o = r.offset;
      if (r.source) sources.push(r.source);
    }
    return { offset: o, ambiguous: sources.length > 0, sources };
  }
}
