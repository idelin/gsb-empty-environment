import type {
  AttributeDomain,
  DomainSpec,
  Input,
  Primitive,
} from "./types.ts";

function valuesOf(domain: AttributeDomain): Primitive[] {
  if (domain.kind === "set") return [...domain.values];
  const out: number[] = [];
  for (let v = domain.min; v <= domain.max; v++) out.push(v);
  return out;
}

/**
 * Enumerate every input point in the declared finite domain.
 * Integer attributes are enumerated only over [min, max] — the analysis
 * scope statement makes this boundary explicit.
 */
export function enumerateInputs(domain: DomainSpec): Input[] {
  const entries = Object.entries(domain);
  let points: Input[] = [{}];
  for (const [attr, dom] of entries) {
    const next: Input[] = [];
    for (const point of points) {
      for (const value of valuesOf(dom)) {
        next.push({ ...point, [attr]: value });
      }
    }
    points = next;
  }
  return points;
}

export function domainSize(domain: DomainSpec): number {
  return Object.values(domain).reduce(
    (acc, dom) =>
      acc * (dom.kind === "set" ? dom.values.length : dom.max - dom.min + 1),
    1,
  );
}

export interface Projection {
  attr: string;
  /** Distinct values this attribute takes inside the region. */
  values: Primitive[];
  /** True when the region spans the whole declared domain on this attribute. */
  spansAll: boolean;
}

/** Set of distinct values taken by each attribute inside a region. */
export function projectRegion(
  points: readonly Input[],
  domain: DomainSpec,
): Projection[] {
  return Object.entries(domain).map(([attr, dom]) => {
    const full = new Set<Primitive>(valuesOf(dom));
    const seen = new Set<Primitive>();
    for (const p of points) seen.add(p[attr] as Primitive);
    const values = [...seen].sort((a, b) =>
      String(a) < String(b) ? -1 : String(a) > String(b) ? 1 : 0,
    );
    const spansAll =
      full.size === seen.size && [...full].every((v) => seen.has(v));
    return { attr, values, spansAll };
  });
}

/** Human readable scope description of the finite analysis universe. */
export function describeScope(domain: DomainSpec): string {
  return Object.entries(domain)
    .map(([attr, dom]) =>
      dom.kind === "set"
        ? `${attr} ∈ {${dom.values.join(", ")}}`
        : `${attr} ∈ [${dom.min}, ${dom.max}] (integer)`,
    )
    .join(", ");
}
