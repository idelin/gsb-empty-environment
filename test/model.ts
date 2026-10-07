/**
 * Independent per-character reference model.
 *
 * Computes the winning attributes for every single character offset by
 * brute force, then groups consecutive equal-styled characters into the
 * expected minimal segments. Used to cross-check MarkerSet.segments().
 */
import type { Attrs, Marker, Segment } from "../src/markers.ts";

function wins(challenger: Marker, holder: Marker): boolean {
  if (challenger.priority !== holder.priority) {
    return challenger.priority > holder.priority;
  }
  if (challenger.sourceId !== holder.sourceId) {
    return challenger.sourceId > holder.sourceId;
  }
  return challenger.id < holder.id;
}

export function modelAttrsAt(markers: readonly Marker[], pos: number): Attrs {
  const winners = new Map<string, Marker>();
  for (const marker of markers) {
    if (marker.start === marker.end) continue; // anchors cover nothing
    if (pos < marker.start || pos >= marker.end) continue; // half-open
    for (const key of Object.keys(marker.attrs)) {
      const holder = winners.get(key);
      if (holder === undefined || wins(marker, holder)) {
        winners.set(key, marker);
      }
    }
  }
  const attrs: Attrs = {};
  for (const [key, marker] of winners) attrs[key] = marker.attrs[key]!;
  return attrs;
}

function sameAttrs(a: Attrs, b: Attrs): boolean {
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  return ka.length === kb.length && ka.every((k) => k in b && a[k] === b[k]);
}

export function modelSegments(
  markers: readonly Marker[],
  from: number,
  to: number,
): Segment[] {
  const segments: Segment[] = [];
  for (let pos = from; pos < to; pos++) {
    const attrs = modelAttrsAt(markers, pos);
    if (Object.keys(attrs).length === 0) continue;
    const last = segments[segments.length - 1];
    if (last !== undefined && last.end === pos && sameAttrs(last.attrs, attrs)) {
      segments[segments.length - 1] = { start: last.start, end: pos + 1, attrs: last.attrs };
    } else {
      segments.push({ start: pos, end: pos + 1, attrs });
    }
  }
  return segments;
}
