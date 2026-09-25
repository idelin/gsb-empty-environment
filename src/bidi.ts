// Minimal bidi support: strong-character direction detection and the
// UBA L2 visual reordering rule over embedding levels. This is a
// deliberate simplification (no explicit codes, two/three levels only)
// sufficient for fixture-driven mixed-direction fragments.

import type { Dir } from './types.ts';

const RTL_RANGES: Array<[number, number]> = [
  [0x0590, 0x05ff], // Hebrew
  [0x0600, 0x06ff], // Arabic
  [0x0750, 0x077f], // Arabic Supplement
  [0x08a0, 0x08ff], // Arabic Extended-A
  [0xfb1d, 0xfb4f], // Hebrew presentation forms
  [0xfb50, 0xfdff], // Arabic presentation forms A
  [0xfe70, 0xfeff], // Arabic presentation forms B
];

export function strongDir(cp: number): Dir | null {
  for (const [lo, hi] of RTL_RANGES) {
    if (cp >= lo && cp <= hi) return 'rtl';
  }
  if (
    (cp >= 0x0041 && cp <= 0x005a) || (cp >= 0x0061 && cp <= 0x007a) ||
    (cp >= 0x00c0 && cp <= 0x024f) || (cp >= 0x0370 && cp <= 0x03ff) ||
    (cp >= 0x0400 && cp <= 0x04ff) || (cp >= 0x4e00 && cp <= 0x9fff) ||
    (cp >= 0x3040 && cp <= 0x30ff) || (cp >= 0xac00 && cp <= 0xd7af)
  ) {
    return 'ltr';
  }
  return null; // weak / neutral (digits, punctuation, spaces, emoji)
}

export function resolveClusterDir(text: string, fallback: Dir): Dir {
  for (const ch of text) {
    const d = strongDir(ch.codePointAt(0) as number);
    if (d) return d;
  }
  // European digits always take an even embedding level so their order
  // is never reversed, mirroring UBA level assignment for EN.
  if (/\d/.test(text)) return 'ltr';
  return fallback;
}

export function paragraphDir(source: string, explicit?: Dir): Dir {
  if (explicit) return explicit;
  for (const ch of source) {
    const d = strongDir(ch.codePointAt(0) as number);
    if (d) return d;
  }
  return 'ltr';
}

// Embedding level for a cluster inside a paragraph: two-level model.
export function levelOf(dir: Dir, paraDir: Dir): number {
  if (paraDir === 'ltr') return dir === 'rtl' ? 1 : 0;
  return dir === 'rtl' ? 1 : 2;
}

// UBA L2: from the highest level down to the lowest odd level, reverse
// every maximal contiguous run of items at or above that level.
export function reorderVisual<T>(logical: T[], levelOfItem: (t: T) => number): T[] {
  const items = logical.slice();
  const levels = logical.map(levelOfItem);
  let max = 0;
  let minOdd = Infinity;
  for (const l of levels) {
    if (l > max) max = l;
    if (l % 2 === 1 && l < minOdd) minOdd = l;
  }
  if (minOdd === Infinity) return items; // uniform even levels: nothing to do
  for (let level = max; level >= minOdd; level--) {
    let i = 0;
    while (i < items.length) {
      if (levels[i] >= level) {
        let j = i;
        while (j < items.length && levels[j] >= level) j++;
        for (let a = i, b = j - 1; a < b; a++, b--) {
          const tmp = items[a]; items[a] = items[b]; items[b] = tmp;
        }
        i = j;
      } else {
        i++;
      }
    }
  }
  return items;
}
