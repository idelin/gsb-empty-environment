import type { ByteRange, CanonicalMapEntry, CanonicalSpec } from "./types.ts";

/**
 * Canonicalization builder used by the acceptance fixtures (and reusable by a
 * host implementing the same transforms).
 *
 * Two transforms are supported, mirroring the task contract:
 *  - foldNewlines: CRLF in raw bytes becomes a single LF canonical byte, so
 *    one canonical coordinate spans two raw bytes;
 *  - stripPadding: bytes equal to `paddingByte` inside `rawRange` disappear
 *    from canonical coordinates but remain addressable in raw bytes.
 *
 * The builder emits the ordered reverse mapping. Adjacent surviving raw runs
 * whose canonical coordinates are also adjacent are coalesced; a fold point
 * deliberately keeps its two raw sides in separate entries so tests can sign
 * either side of the folded character independently.
 */
export interface BuildOptions {
  version: string;
  foldNewlines: boolean;
  stripPadding: boolean;
  paddingByte?: number;
  /** Mark this builder's output as deliberately ambiguous for negative tests. */
  forceAmbiguous?: boolean;
}

export function buildCanonicalSpec(
  canonicalId: string,
  rawRange: ByteRange,
  rawBytes: Uint8Array,
  options: BuildOptions,
): { id: string; spec: CanonicalSpec; canonicalBytes: Uint8Array } {
  const paddingByte = options.paddingByte ?? 0x00;
  // Emit one entry per surviving canonical byte, then coalesce adjacent
  // same-shape entries. Fold points (two raw bytes -> one canonical byte) and
  // removed padding (no entry) naturally stay distinct from normal runs.
  const entries: CanonicalMapEntry[] = [];
  const canonical: number[] = [];
  let canonicalPos = 0;
  for (let offset = rawRange.start; offset < rawRange.end; ) {
    const byte = rawBytes[offset];
    if (options.stripPadding && byte === paddingByte) {
      offset += 1;
      continue;
    }
    if (options.foldNewlines && byte === 0x0d && rawBytes[offset + 1] === 0x0a) {
      entries.push({
        start: offset,
        end: offset + 2,
        canonicalStart: canonicalPos,
        canonicalEnd: canonicalPos + 1,
      });
      canonical.push(0x0a);
      canonicalPos += 1;
      offset += 2;
      continue;
    }
    entries.push({
      start: offset,
      end: offset + 1,
      canonicalStart: canonicalPos,
      canonicalEnd: canonicalPos + 1,
    });
    canonical.push(byte);
    canonicalPos += 1;
    offset += 1;
  }

  const coalesced: CanonicalMapEntry[] = [];
  for (const entry of entries) {
    const last = coalesced[coalesced.length - 1];
    const bothNormal =
      last &&
      last.end - last.start === 1 &&
      entry.end - entry.start === 1 &&
      last.end === entry.start &&
      last.canonicalEnd === entry.canonicalStart;
    if (bothNormal) {
      last.end = entry.end;
      last.canonicalEnd = entry.canonicalEnd;
    } else {
      coalesced.push({ ...entry });
    }
  }
  entries.length = 0;
  entries.push(...coalesced);

  if (options.forceAmbiguous) {
    // Two canonical positions pointing at the same raw interval, placed in a
    // free canonical gap so ordering checks do not mask the ambiguity.
    const first = entries[0];
    if (first) {
      const gapStart = canonicalPos + 2;
      entries.push({ ...first, canonicalStart: gapStart, canonicalEnd: gapStart + 1 });
    }
  }

  const spec: CanonicalSpec = {
    version: options.version,
    foldNewlines: options.foldNewlines,
    stripPadding: options.stripPadding,
    rawRange,
    reverseMap: entries,
  };
  return { id: canonicalId, spec, canonicalBytes: Uint8Array.from(canonical) };
}
