import { Attrs, Marker } from '../src/markers';

/**
 * Independent per-character oracle: resolves attributes one character at a
 * time, straight from the spec, with no shared code with the implementation.
 * Used to cross-check MarkerSet output.
 */

function stronger(a: Marker, b: Marker): Marker {
  if (a.priority !== b.priority) return a.priority > b.priority ? a : b;
  if (a.source !== b.source) return a.source > b.source ? a : b;
  return a.id > b.id ? a : b;
}

/** Final attributes at character offset `pos`, per the spec. */
export function attrsAt(markers: readonly Marker[], pos: number): Attrs {
  const winners = new Map<string, Marker>();
  for (const m of markers) {
    if (m.start >= m.end) continue; // empty ranges are anchors, occupy nothing
    if (pos < m.start || pos >= m.end) continue;
    for (const key of Object.keys(m.attrs)) {
      const w = winners.get(key);
      winners.set(key, w ? stronger(m, w) : m);
    }
  }
  const out: Attrs = {};
  for (const [key, m] of winners) out[key] = m.attrs[key];
  return out;
}

/** Offsets at which anchors sit, with their marker ids (strongest first). */
export function anchorsAt(
  markers: readonly Marker[],
): Array<{ offset: number; ids: string[] }> {
  const byOffset = new Map<number, Marker[]>();
  for (const m of markers) {
    if (m.start !== m.end) continue;
    const list = byOffset.get(m.start);
    if (list) list.push(m);
    else byOffset.set(m.start, [m]);
  }
  return [...byOffset.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([offset, ms]) => ({
      offset,
      ids: ms.sort((x, y) => (stronger(x, y) === x ? -1 : 1)).map((m) => m.id),
    }));
}
