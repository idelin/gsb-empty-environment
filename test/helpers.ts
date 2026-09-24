/**
 * Test oracle: an independent per-character identity model.
 *
 * Selection endpoints are expressed as gaps between character ids. Remapping
 * a gap across an edit never looks at offsets — it only follows surviving
 * atom ids — so comparing `gapToOffset(remapGap(...))` against
 * `Mapping.mapOffset(...)` validates the offset-based mapping against pure
 * character identity.
 */

import { TextDocument, type Atom } from '../src/document.ts';
import type { Affinity } from '../src/mapping.ts';

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface Gap {
  readonly leftId: number | null;
  readonly rightId: number | null;
  readonly affinity: Affinity;
}

export function endpointToGap(doc: TextDocument, offset: number, affinity: Affinity): Gap {
  const { index } = doc.atomIndexAt(offset);
  return {
    leftId: index > 0 ? doc.atoms[index - 1].id : null,
    rightId: index < doc.atoms.length ? doc.atoms[index].id : null,
    affinity,
  };
}

export function gapToOffset(doc: TextDocument, gap: Gap): number {
  if (gap.rightId !== null) {
    const idx = doc.atoms.findIndex((a) => a.id === gap.rightId);
    return doc.starts()[idx];
  }
  return doc.length;
}

function indexOfId(atoms: readonly Atom[], id: number): number {
  return atoms.findIndex((a) => a.id === id);
}

/** Remap a gap from `before` to `after` using only atom identity. */
export function remapGap(before: TextDocument, after: TextDocument, gap: Gap): Gap {
  const afterIds = new Set(after.atoms.map((a) => a.id));

  let li = gap.leftId === null ? -1 : indexOfId(before.atoms, gap.leftId);
  while (li >= 0 && !afterIds.has(before.atoms[li].id)) li--;

  let ri = gap.rightId === null ? before.atoms.length : indexOfId(before.atoms, gap.rightId);
  while (ri < before.atoms.length && !afterIds.has(before.atoms[ri].id)) ri++;

  let leftId = li >= 0 ? before.atoms[li].id : null;
  let rightId = ri < before.atoms.length ? before.atoms[ri].id : null;

  const posL = leftId === null ? -1 : indexOfId(after.atoms, leftId);
  const posR = rightId === null ? after.atoms.length : indexOfId(after.atoms, rightId);
  if (posR - posL > 1) {
    // Inserted atoms sit inside the gap: stickiness decides which side we keep.
    if (gap.affinity === 'after') leftId = after.atoms[posR - 1].id;
    else rightId = after.atoms[posL + 1].id;
  }
  return { leftId, rightId, affinity: gap.affinity };
}

/** Alphabet mixing BMP, surrogate pairs and both newline styles. */
export const FUZZ_ALPHABET = ['a', 'b', 'Z', '0', ' ', '\n', '\r', '\u{1F600}', '\u{1F4A9}', 'é', '中'];

export function randomText(rand: () => number, maxLen: number): string {
  const n = Math.floor(rand() * maxLen);
  let s = '';
  for (let i = 0; i < n; i++) s += FUZZ_ALPHABET[Math.floor(rand() * FUZZ_ALPHABET.length)];
  return s;
}
