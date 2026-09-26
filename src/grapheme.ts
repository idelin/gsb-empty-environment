/**
 * Minimal grapheme-cluster segmentation.
 *
 * This is deliberately NOT a full UAX #29 implementation: it keeps
 * combining marks, ZWJ sequences and regional-indicator pairs together,
 * which is all the fixtures in this project require. A production system
 * may inject its own clusters via `TextNode.clusters` instead.
 */

const EXTENDED_PICTOGRAPHIC = new Set([
  0x1f300, 0x1f301, 0x1f302, 0x1f303, 0x1f304, 0x1f305, 0x1f306, 0x1f307,
  0x1f308, 0x1f309, 0x1f30a, 0x1f30b, 0x1f30c, 0x1f30d, 0x1f30e, 0x1f30f,
  0x1f310, 0x1f311, 0x1f600, 0x1f601, 0x1f602, 0x1f603, 0x1f44b, 0x1f466,
  0x1f467, 0x1f468, 0x1f469, 0x1f9d1, 0x2705, 0x2614, 0x2615,
]);

const isRegionalIndicator = (cp: number): boolean =>
  cp >= 0x1f1e6 && cp <= 0x1f1ff;

const isCombiningMark = (cp: number): boolean =>
  (cp >= 0x0300 && cp <= 0x036f) ||
  (cp >= 0x1ab0 && cp <= 0x1aff) ||
  (cp >= 0x1dc0 && cp <= 0x1dff) ||
  (cp >= 0x20d0 && cp <= 0x20ff) ||
  (cp >= 0xfe20 && cp <= 0xfe2f);

const isZWJ = (cp: number): boolean => cp === 0x200d;
const isVS = (cp: number): boolean => cp >= 0xfe00 && cp <= 0xfe0f;

function codepoints(text: string): number[] {
  return Array.from(text, (ch) => ch.codePointAt(0) ?? 0);
}

export function splitGraphemes(text: string): string[] {
  const cps = codepoints(text);
  const clusters: string[] = [];
  let i = 0;
  while (i < cps.length) {
    const start = i;
    i += 1;
    // Extend with marks / variation selectors / ZWJ chains.
    while (i < cps.length) {
      const cp = cps[i]!;
      if (isCombiningMark(cp) || isVS(cp) || isZWJ(cp)) {
        i += 1;
        if (isZWJ(cp) && i < cps.length) {
          // consume the element joined by ZWJ, then the chain may continue
          i += 1;
        }
        continue;
      }
      break;
    }
    // Regional indicator pair (flag): consume the second RI after a base RI.
    if (isRegionalIndicator(cps[start]!) && i < cps.length && isRegionalIndicator(cps[i]!)) {
      i += 1;
    }
    // Emoji + VS16 / ZWJ sequences are already consumed by the extension loop.
    void EXTENDED_PICTOGRAPHIC;
    clusters.push(String.fromCodePoint(...cps.slice(start, i)));
  }
  return clusters;
}
