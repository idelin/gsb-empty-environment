import type { ByteRange, CanonicalMapEntry, CanonicalSpec } from "./types.ts";

/** A range is valid when half-open, ordered and inside the raw container. */
export function isValidRange(range: ByteRange, rawLength: number): boolean {
  return (
    Number.isInteger(range.start) &&
    Number.isInteger(range.end) &&
    range.start >= 0 &&
    range.end <= rawLength &&
    range.start <= range.end
  );
}

/** True when `outer` fully contains `inner` (half-open semantics). */
export function contains(outer: ByteRange, inner: ByteRange): boolean {
  return outer.start <= inner.start && inner.end <= outer.end;
}

/** True when every required range is contained by the candidate range. */
export function coversAll(candidate: ByteRange, required: ByteRange[]): boolean {
  return required.every((range) => contains(candidate, range));
}

/** True when two ranges share at least one byte. */
export function overlaps(a: ByteRange, b: ByteRange): boolean {
  return a.start < b.end && b.start < a.end;
}

/**
 * Validate a host-supplied reverse map.
 *
 * The mapping is ambiguous when:
 *  1. entries are unordered or overlap in canonical coordinates, or
 *  2. one canonical coordinate maps back to more than one distinct original
 *     byte interval (the host cannot give a unique raw location), or
 *  3. mapped raw intervals fall outside the canonicalization's raw range.
 *
 * Duplicate path aliases are NOT ambiguity: two regions may legitimately
 * reference identical raw bytes. Ambiguity is a property of one mapping,
 * decided here before any signature is evaluated.
 */
export function validateCanonicalMap(
  spec: CanonicalSpec,
): { ok: true } | { ok: false; reason: string } {
  const entries = spec.reverseMap;
  let previousEnd = 0;
  const seenRaw = new Map<string, number>();
  const seenCanonical = new Map<string, number>();

  for (const entry of entries) {
    if (
      entry.canonicalStart < previousEnd ||
      entry.canonicalStart >= entry.canonicalEnd
    ) {
      return {
        ok: false,
        reason: `unordered or overlapping canonical interval [${entry.canonicalStart}, ${entry.canonicalEnd})`,
      };
    }
    if (!contains(spec.rawRange, entry)) {
      return {
        ok: false,
        reason: `raw interval [${entry.start}, ${entry.end}) escapes canonical raw range`,
      };
    }
    const canonicalKey = `${entry.canonicalStart}:${entry.canonicalEnd - entry.canonicalStart}`;
    const priorCanonical = seenCanonical.get(canonicalKey);
    if (priorCanonical !== undefined && priorCanonical !== entry.start) {
      return {
        ok: false,
        reason: `canonical interval [${entry.canonicalStart}, ${entry.canonicalEnd}) maps to multiple distinct raw intervals`,
      };
    }
    seenCanonical.set(canonicalKey, entry.start);
    const key = `${entry.start}:${entry.end - entry.start}`;
    const prior = seenRaw.get(key);
    if (prior !== undefined && prior !== entry.canonicalStart) {
      return {
        ok: false,
        reason: `raw interval [${entry.start}, ${entry.end}) maps from multiple canonical positions`,
      };
    }
    seenRaw.set(key, entry.canonicalStart);
    previousEnd = entry.canonicalEnd;
  }
  return { ok: true };
}

/** Build a simple identity reverse map for a raw range (no transforms). */
export function identityMap(spec: {
  rawRange: ByteRange;
}): CanonicalMapEntry[] {
  return [
    {
      start: spec.rawRange.start,
      end: spec.rawRange.end,
      canonicalStart: 0,
      canonicalEnd: spec.rawRange.end - spec.rawRange.start,
    },
  ];
}
