import type { BlockNode, Document, GlyphMetrics, PageConfig } from "./model.ts";

export function fixtureMetrics(): GlyphMetrics {
  return {
    lineHeight: 12,
    spaceWidth: 4,
    charWidth: () => 8,
  };
}

export function sampleConfig(): PageConfig {
  return {
    pageHeight: 120,
    lineWidth: 480,
    orphans: 2,
    widows: 2,
    keepWithNext: true,
    footnoteSeparatorHeight: 6,
    paragraphSplitPenalty: 25,
    whitespaceWeight: 1,
  };
}

export function makeWords(seed: number, count: number): string {
  const words: string[] = [];
  for (let i = 0; i < count; i++) {
    const len = ((seed * 31 + i * 7) % 5) + 3;
    let word = "";
    for (let k = 0; k < len; k++) {
      word += String.fromCharCode(97 + ((seed * 13 + i * 5 + k * 3) % 26));
    }
    words.push(word);
  }
  return words.join(" ");
}

export function para(
  id: string,
  wordCount: number,
  opts: { seed?: number; footnoteRef?: string; refAtWord?: number } = {},
): BlockNode {
  const seed = opts.seed ?? wordCount;
  const words = makeWords(seed, wordCount).split(" ");
  if (!opts.footnoteRef) {
    return { kind: "paragraph", id, spans: [{ text: words.join(" ") }] };
  }
  const at = Math.min(opts.refAtWord ?? 0, words.length - 1);
  return {
    kind: "paragraph",
    id,
    spans: [
      { text: words.slice(0, at).join(" ") },
      { text: words[at], footnoteRef: opts.footnoteRef },
      { text: words.slice(at + 1).join(" ") },
    ].filter((s) => s.text.length > 0),
  };
}

export function heading(id: string, text: string, level = 1): BlockNode {
  return { kind: "heading", id, text, level };
}

export function block(id: string, height: number): BlockNode {
  return { kind: "block", id, height };
}

export function sampleDocument(): Document {
  return {
    nodes: [
      heading("h1", "Overview"),
      para("p1", 40, { seed: 3, footnoteRef: "f1", refAtWord: 6 }),
      para("p2", 38, { seed: 11 }),
      block("b1", 36),
      para("p3", 48, { seed: 7, footnoteRef: "f2", refAtWord: 20 }),
      heading("h2", "Details", 2),
      para("p4", 30, { seed: 5 }),
    ],
    footnotes: [
      { id: "f1", text: makeWords(21, 12) },
      { id: "f2", text: makeWords(33, 6) },
    ],
  };
}
