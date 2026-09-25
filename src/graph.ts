import {
  mat3Invert,
  mat3IsIdentity,
  mat3MaxAbsDiff,
  mat3Multiply,
  SingularMatrixError,
  type Mat3,
} from './matrix.ts';
import { adaptationMatrix, whitePointsEqual } from './adapt.ts';
import { rgbToXyzMatrix, xyzToRgbMatrix, type ColorSpaceDef } from './spaces.ts';
import { hashVersion, stableStringify } from './version.ts';

export class CycleContradictionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CycleContradictionError';
  }
}

export class PathContradictionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PathContradictionError';
  }
}

export class UnknownSpaceError extends Error {
  constructor(id: string) {
    super(`unknown color space: ${id}`);
    this.name = 'UnknownSpaceError';
  }
}

/** A user-registered direct linear-stage transform between two spaces. */
export interface ExplicitEdge {
  from: string;
  to: string;
  matrix: Mat3;
  label?: string;
}

export interface PathCheck {
  from: string;
  to: string;
  maxDeviation: number;
}

export interface CompiledGraph {
  readonly spaces: ReadonlyMap<string, ColorSpaceDef>;
  readonly edges: readonly ExplicitEdge[];
  readonly pathChecks: readonly PathCheck[];
  readonly fingerprint: string;
  findEdge(from: string, to: string): ExplicitEdge | undefined;
  hubMatrix(from: string, to: string): Mat3;
  requiresAdaptation(from: string, to: string): boolean;
}

const DEFAULT_PATH_TOLERANCE = 1e-6;
const CYCLE_TOLERANCE = 1e-9;

/**
 * Conversion graph over color spaces. The XYZ hub path is derived
 * automatically from each space's primaries/white point; explicit edges
 * may declare direct transforms which are validated at compile time:
 *  - every edge matrix must be non-singular and invertible;
 *  - every cycle of explicit edges must compose to the identity
 *    (otherwise the cycle is contradictory);
 *  - every explicit edge must agree with the derived hub path
 *    (otherwise the two theoretically equivalent paths contradict).
 */
export class ConversionGraph {
  private readonly spaces = new Map<string, ColorSpaceDef>();
  private readonly edges: ExplicitEdge[] = [];

  addSpace(def: ColorSpaceDef): this {
    if (this.spaces.has(def.id)) throw new Error(`duplicate color space: ${def.id}`);
    this.spaces.set(def.id, def);
    return this;
  }

  addEdge(edge: ExplicitEdge): this {
    if (!this.spaces.has(edge.from)) throw new UnknownSpaceError(edge.from);
    if (!this.spaces.has(edge.to)) throw new UnknownSpaceError(edge.to);
    // Singularity + invertibility are checked eagerly: M * M^-1 must be I.
    const inverse = mat3Invert(edge.matrix);
    if (!mat3IsIdentity(mat3Multiply(edge.matrix, inverse), 1e-9)) {
      throw new SingularMatrixError(
        `edge ${edge.from} -> ${edge.to} failed the invertibility check`,
      );
    }
    this.edges.push(edge);
    return this;
  }

  requireSpace(id: string): ColorSpaceDef {
    const def = this.spaces.get(id);
    if (!def) throw new UnknownSpaceError(id);
    return def;
  }

  /**
   * Derived linear-stage matrix: source linear RGB -> XYZ -> (chromatic
   * adaptation when white points differ) -> target linear RGB.
   */
  hubMatrix(from: string, to: string): Mat3 {
    const src = this.requireSpace(from);
    const dst = this.requireSpace(to);
    const adapt = adaptationMatrix(src.white, dst.white);
    return mat3Multiply(
      xyzToRgbMatrix(dst),
      mat3Multiply(adapt, rgbToXyzMatrix(src)),
    );
  }

  compile(options: { pathTolerance?: number } = {}): CompiledGraph {
    const pathTolerance = options.pathTolerance ?? DEFAULT_PATH_TOLERANCE;

    // 1. Every space must yield non-singular, invertible matrices.
    for (const def of this.spaces.values()) {
      const m = rgbToXyzMatrix(def);
      const inv = mat3Invert(m); // throws SingularMatrixError
      if (!mat3IsIdentity(mat3Multiply(m, inv), 1e-9)) {
        throw new SingularMatrixError(`space ${def.id} produced a non-invertible RGB<->XYZ matrix`);
      }
    }

    // 2. Cycle contradiction check over explicit edges.
    for (const edge of this.edges) {
      const pathMatrix = this.composePath(edge.to, edge.from, new Set([edge.from]));
      if (pathMatrix !== null) {
        const loop = mat3Multiply(pathMatrix, edge.matrix);
        if (!mat3IsIdentity(loop, CYCLE_TOLERANCE)) {
          throw new CycleContradictionError(
            `cycle through ${edge.from} -> ${edge.to} composes to a non-identity transform ` +
              `(max deviation ${mat3MaxAbsDiff(loop, [1, 0, 0, 0, 1, 0, 0, 0, 1]).toExponential(3)})`,
          );
        }
      }
    }

    // 3. Explicit edges must agree with the derived hub path.
    const pathChecks: PathCheck[] = [];
    for (const edge of this.edges) {
      const derived = this.hubMatrix(edge.from, edge.to);
      const maxDeviation = mat3MaxAbsDiff(edge.matrix, derived);
      pathChecks.push({ from: edge.from, to: edge.to, maxDeviation });
      if (maxDeviation > pathTolerance) {
        throw new PathContradictionError(
          `explicit edge ${edge.from} -> ${edge.to} deviates from the derived XYZ-hub path ` +
            `by ${maxDeviation.toExponential(3)} (tolerance ${pathTolerance})`,
        );
      }
    }

    const spaces = new Map(this.spaces);
    const edges = [...this.edges];
    const fingerprint = hashVersion(
      stableStringify({
        spaces: [...spaces.values()].map((s) => ({
          id: s.id,
          kind: s.kind,
          primaries: s.primaries,
          white: s.white,
          transfer: s.transfer.id,
        })),
        edges: edges.map((e) => ({ from: e.from, to: e.to, matrix: e.matrix })),
      }),
    );

    const self = this;
    return {
      spaces,
      edges,
      pathChecks,
      fingerprint,
      findEdge(from, to) {
        return edges.find((e) => e.from === from && e.to === to);
      },
      hubMatrix(from, to) {
        return self.hubMatrix(from, to);
      },
      requiresAdaptation(from, to) {
        return !whitePointsEqual(self.requireSpace(from).white, self.requireSpace(to).white);
      },
    };
  }

  /** Composes explicit-edge matrices along a path from -> to, or null. */
  private composePath(from: string, to: string, visited: Set<string>): Mat3 | null {
    if (from === to) return [1, 0, 0, 0, 1, 0, 0, 0, 1];
    for (const edge of this.edges) {
      if (edge.from !== from) continue;
      if (edge.to !== to && visited.has(edge.to)) continue;
      const rest = this.composePath(edge.to, to, new Set([...visited, edge.to]));
      if (rest !== null) return mat3Multiply(rest, edge.matrix);
    }
    return null;
  }
}
