import type { DocNode, FlatCluster, GlyphMetrics } from "../src/types.ts";

/** Fixed advance width per cluster — deterministic, no font engine. */
export function fixedMetrics(width: number, lineHeight = 16): GlyphMetrics {
  return { lineHeight, clusterWidth: (_c: FlatCluster) => width };
}

/** Width table keyed by cluster text, falling back to `fallback`. */
export function tableMetrics(table: Record<string, number>, fallback = 10, lineHeight = 16): GlyphMetrics {
  return { lineHeight, clusterWidth: (c: FlatCluster) => table[c.text] ?? fallback };
}

/**
 * The canonical fixture: "abcDEFghi" with explicit levels
 *   a b c [ D E F ] g h i   levels: 0 0 0 1 1 1 0 0 0
 * (LTR paragraph embedding an RTL fragment.)
 */
export function mixedTree(): DocNode {
  return {
    id: "p",
    children: [
      {
        id: "t",
        text: "abcDEFghi",
        clusters: ["a", "b", "c", "D", "E", "F", "g", "h", "i"],
        levels: [0, 0, 0, 1, 1, 1, 0, 0, 0],
      },
    ],
  };
}

/** RTL paragraph embedding an LTR fragment: levels 1 1 [ 2 2 2 ] 1 1. */
export function nestedTree(): DocNode {
  return {
    id: "p",
    children: [
      {
        id: "t",
        level: 1,
        text: "heWORLDab",
        clusters: ["h", "e", "W", "O", "R", "L", "D", "a", "b"],
        levels: [1, 1, 2, 2, 2, 2, 2, 1, 1],
      },
    ],
  };
}

/** Grapheme atomicity fixture: combining mark, ZWJ family, flag pair. */
export function graphemeTree(): DocNode {
  return {
    id: "p",
    children: [
      {
        id: "t",
        text: ["e", "e\u0301", "\u{1F468}\u200D\u{1F469}", "\u{1F1FA}\u{1F1F8}"].join(""),
        level: 0,
      },
    ],
  };
}
