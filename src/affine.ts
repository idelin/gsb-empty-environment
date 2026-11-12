import { inputError, TrajectoryRemapError } from "./errors.ts";
import type { AffineMatrix, Point } from "./types.ts";

const IDENTITY: AffineMatrix = { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null ? (value as Record<string, unknown>) : undefined;
}

function finiteMatrix(values: unknown): AffineMatrix | undefined {
  if (!Array.isArray(values) || values.length !== 6 || !values.every((value) => typeof value === "number" && Number.isFinite(value))) {
    return undefined;
  }
  const [a, b, c, d, e, f] = values as number[];
  return { a, b, c, d, e, f };
}

export function identityMatrix(): AffineMatrix {
  return { ...IDENTITY };
}

export function applyAffineMatrix(matrix: AffineMatrix, point: Point): Point {
  return {
    x: matrix.a * point.x + matrix.c * point.y + matrix.e,
    y: matrix.b * point.x + matrix.d * point.y + matrix.f,
  };
}

export function composeAffineMatrices(outer: AffineMatrix, inner: AffineMatrix): AffineMatrix {
  return {
    a: outer.a * inner.a + outer.c * inner.b,
    b: outer.b * inner.a + outer.d * inner.b,
    c: outer.a * inner.c + outer.c * inner.d,
    d: outer.b * inner.c + outer.d * inner.d,
    e: outer.a * inner.e + outer.c * inner.f + outer.e,
    f: outer.b * inner.e + outer.d * inner.f + outer.f,
  };
}

export function invertAffineMatrix(matrix: AffineMatrix): AffineMatrix {
  const determinant = matrix.a * matrix.d - matrix.b * matrix.c;
  if (!Number.isFinite(determinant) || Math.abs(determinant) <= 1e-15) {
    throw new TrajectoryRemapError("INVALID_TRANSFORM", "Affine transform is singular and cannot be inverted");
  }

  const a = matrix.d / determinant;
  const b = -matrix.b / determinant;
  const c = -matrix.c / determinant;
  const d = matrix.a / determinant;
  const e = -(a * matrix.e + c * matrix.f);
  const f = -(b * matrix.e + d * matrix.f);
  return { a, b, c, d, e, f };
}

function matrixFromRows(rows: readonly (readonly unknown[])[]): AffineMatrix | undefined {
  if (rows.length === 2 && rows.every((row) => Array.isArray(row) && row.length === 3)) {
    const [first, second] = rows as unknown as [number[], number[]];
    if ([...first, ...second].every((value) => typeof value === "number" && Number.isFinite(value))) {
      return { a: first[0], b: second[0], c: first[1], d: second[1], e: first[2], f: second[2] };
    }
  }

  if (rows.length === 3 && rows.every((row) => Array.isArray(row) && row.length === 3)) {
    const first = rows[0] as number[];
    const second = rows[1] as number[];
    if ([...first, ...second].every((value) => typeof value === "number" && Number.isFinite(value))) {
      return { a: first[0], b: second[0], c: first[1], d: second[1], e: first[2], f: second[2] };
    }
  }

  return undefined;
}

function numberPair(value: unknown): [number, number] | undefined {
  if (Array.isArray(value) && value.length >= 2 && typeof value[0] === "number" && typeof value[1] === "number") {
    return [value[0], value[1]];
  }
  const objectValue = record(value);
  if (objectValue) {
    const first = typeof objectValue.x === "number" ? objectValue.x : objectValue.sx;
    const second = typeof objectValue.y === "number" ? objectValue.y : objectValue.sy;
    if (typeof first === "number" && typeof second === "number") {
      return [first, second];
    }
  }
  return undefined;
}

function matrixFromParts(value: Record<string, unknown>): AffineMatrix | undefined {
  const hasScale =
    value.scale !== undefined ||
    value.scaleX !== undefined ||
    value.scaleY !== undefined ||
    value.sx !== undefined ||
    value.sy !== undefined ||
    value.dpiScale !== undefined ||
    value.dpi !== undefined ||
    (value.fromDpi !== undefined && value.toDpi !== undefined);
  const hasRotation =
    value.rotation !== undefined ||
    value.rotate !== undefined ||
    value.rotationDeg !== undefined ||
    value.rotationRad !== undefined ||
    value.angleRad !== undefined ||
    value.rotationRadians !== undefined ||
    value.angle !== undefined;
  const hasTranslation =
    value.translate !== undefined ||
    value.translation !== undefined ||
    value.translateX !== undefined ||
    value.translateY !== undefined ||
    value.tx !== undefined ||
    value.ty !== undefined;

  if (!hasScale && !hasRotation && !hasTranslation) {
    return undefined;
  }

  let scaleX = 1;
  let scaleY = 1;
  if (typeof value.scale === "number") {
    scaleX = value.scale;
    scaleY = value.scale;
  } else {
    const pair = numberPair(value.scale);
    if (pair) {
      [scaleX, scaleY] = pair;
    }
  }
  if (typeof value.scaleX === "number") {
    scaleX = value.scaleX;
  }
  if (typeof value.scaleY === "number") {
    scaleY = value.scaleY;
  }
  if (typeof value.sx === "number") {
    scaleX = value.sx;
  }
  if (typeof value.sy === "number") {
    scaleY = value.sy;
  }
  if (typeof value.dpiScale === "number") {
    scaleX = value.dpiScale;
    scaleY = value.dpiScale;
  }
  if (typeof value.dpi === "number" && value.dpi > 0) {
    scaleX = value.dpi / 96;
    scaleY = value.dpi / 96;
  }
  if (typeof value.fromDpi === "number" && typeof value.toDpi === "number" && value.fromDpi !== 0) {
    scaleX = value.toDpi / value.fromDpi;
    scaleY = value.toDpi / value.fromDpi;
  }

  let rotation = 0;
  if (typeof value.rotationRad === "number") {
    rotation = value.rotationRad;
  } else if (typeof value.rotationRadians === "number") {
    rotation = value.rotationRadians;
  } else if (typeof value.angleRad === "number") {
    rotation = value.angleRad;
  } else if (typeof value.rotationDeg === "number") {
    rotation = (value.rotationDeg * Math.PI) / 180;
  } else if (typeof value.rotation === "number") {
    rotation =
      value.rotationUnit === "rad" || value.angleUnit === "rad"
        ? value.rotation
        : (value.rotation * Math.PI) / 180;
  } else if (typeof value.rotate === "number") {
    rotation = value.rotationUnit === "rad" || value.angleUnit === "rad" ? value.rotate : (value.rotate * Math.PI) / 180;
  } else if (typeof value.angle === "number") {
    rotation = value.rotationUnit === "rad" || value.angleUnit === "rad" ? value.angle : (value.angle * Math.PI) / 180;
  }

  let translateX = 0;
  let translateY = 0;
  const translatePair = numberPair(value.translate ?? value.translation);
  if (translatePair) {
    [translateX, translateY] = translatePair;
  }
  if (typeof value.translateX === "number") {
    translateX = value.translateX;
  }
  if (typeof value.translateY === "number") {
    translateY = value.translateY;
  }
  if (typeof value.tx === "number") {
    translateX = value.tx;
  }
  if (typeof value.ty === "number") {
    translateY = value.ty;
  }

  const cosine = Math.cos(rotation);
  const sine = Math.sin(rotation);
  return {
    a: cosine * scaleX,
    b: sine * scaleX,
    c: -sine * scaleY,
    d: cosine * scaleY,
    e: translateX,
    f: translateY,
  };
}

function directMatrix(value: Record<string, unknown>): AffineMatrix | undefined {
  if (["a", "b", "c", "d", "e", "f"].every((key) => typeof value[key] === "number" && Number.isFinite(value[key]))) {
    return {
      a: value.a as number,
      b: value.b as number,
      c: value.c as number,
      d: value.d as number,
      e: value.e as number,
      f: value.f as number,
    };
  }

  if (["m11", "m12", "m21", "m22", "dx", "dy"].every((key) => typeof value[key] === "number" && Number.isFinite(value[key]))) {
    return {
      a: value.m11 as number,
      b: value.m12 as number,
      c: value.m21 as number,
      d: value.m22 as number,
      e: (value.dx ?? 0) as number,
      f: (value.dy ?? 0) as number,
    };
  }

  if (["m11", "m12", "m21", "m22", "m31", "m32"].every((key) => typeof value[key] === "number" && Number.isFinite(value[key]))) {
    return {
      a: value.m11 as number,
      b: value.m12 as number,
      c: value.m21 as number,
      d: value.m22 as number,
      e: value.m31 as number,
      f: value.m32 as number,
    };
  }

  return undefined;
}

/**
 * Accepts DOMMatrix-like objects, six-number tuples, 2x3/3x3 row matrices,
 * `{matrix: ...}` wrappers, and convenience scale/rotation/translation objects.
 * `recordedTransform` + `currentTransform` (or `from` + `to`) are composed as
 * `current * inverse(recorded)`.
 */
export function normalizeAffineTransform(value: unknown, depth = 0): AffineMatrix {
  if (depth > 8) {
    throw new TrajectoryRemapError("INVALID_TRANSFORM", "Affine transform nesting is too deep");
  }

  if (value === undefined || value === null) {
    return identityMatrix();
  }

  const six = finiteMatrix(value);
  if (six) {
    return six;
  }

  if (Array.isArray(value)) {
    if (value.length === 9 && value.every((entry) => typeof entry === "number" && Number.isFinite(entry))) {
      const rows = [
        value.slice(0, 3) as number[],
        value.slice(3, 6) as number[],
        value.slice(6, 9) as number[],
      ];
      const flatMatrix = matrixFromRows(rows);
      if (flatMatrix) {
        return flatMatrix;
      }
    }
    if (value.length === 2) {
      const recorded = finiteMatrix(value[0]);
      const current = finiteMatrix(value[1]);
      if (recorded && current) {
        return composeAffineMatrices(current, invertAffineMatrix(recorded));
      }
    }
    if (value.every((row) => Array.isArray(row))) {
      const rowsMatrix = matrixFromRows(value as readonly (readonly unknown[])[]);
      if (rowsMatrix) {
        return rowsMatrix;
      }
    }
    throw new TrajectoryRemapError("INVALID_TRANSFORM", "Affine array must be [a,b,c,d,e,f] or a 2x3/3x3 matrix");
  }

  const objectValue = record(value);
  if (!objectValue) {
    throw new TrajectoryRemapError("INVALID_TRANSFORM", "Affine transform must be a matrix or matrix-like object");
  }

  const direct = directMatrix(objectValue);
  if (direct) {
    return direct;
  }

  for (const key of ["matrix", "m", "affine", "transform", "coordinateTransform", "windowTransform"]) {
    if (objectValue[key] !== undefined) {
      return normalizeAffineTransform(objectValue[key], depth + 1);
    }
  }

  const recorded = objectValue.recordedTransform ?? objectValue.from ?? objectValue.recorded;
  const current = objectValue.currentTransform ?? objectValue.to ?? objectValue.current;
  if (recorded !== undefined && current !== undefined) {
    const recordedMatrix = normalizeAffineTransform(recorded, depth + 1);
    const currentMatrix = normalizeAffineTransform(current, depth + 1);
    return composeAffineMatrices(currentMatrix, invertAffineMatrix(recordedMatrix));
  }

  const parts = matrixFromParts(objectValue);
  if (parts) {
    return validateMatrix(parts);
  }

  throw new TrajectoryRemapError("INVALID_TRANSFORM", "Could not interpret affine transform");
}

function validateMatrix(matrix: AffineMatrix): AffineMatrix {
  const values = [matrix.a, matrix.b, matrix.c, matrix.d, matrix.e, matrix.f];
  if (!values.every((value) => typeof value === "number" && Number.isFinite(value))) {
    throw new TrajectoryRemapError("INVALID_TRANSFORM", "Affine transform contains a non-finite value");
  }
  return matrix;
}

export function transformPoint(matrix: AffineMatrix, point: Point): Point {
  return applyAffineMatrix(matrix, point);
}

export function transformPoints(matrix: AffineMatrix, points: readonly Point[]): Point[] {
  return points.map((point) => applyAffineMatrix(matrix, point));
}

export function multiplyAffineMatrices(left: AffineMatrix, right: AffineMatrix): AffineMatrix {
  if (!left || !right) {
    throw inputError("Both affine matrices are required");
  }
  return composeAffineMatrices(left, right);
}
