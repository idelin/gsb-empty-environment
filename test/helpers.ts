import type { BlockNode, GlyphMetrics, PageConfig } from "../src/model.ts";

export const testMetrics: GlyphMetrics = {
  lineHeight: 10,
  spaceWidth: 0,
  charWidth: () => 10,
};

export function testConfig(overrides: Partial<PageConfig> = {}): PageConfig {
  return {
    pageHeight: 45,
    lineWidth: 500,
    orphans: 2,
    widows: 2,
    keepWithNext: true,
    footnoteSeparatorHeight: 2,
    paragraphSplitPenalty: 25,
    whitespaceWeight: 1,
    ...overrides,
  };
}

export function words(n: number): string {
  return Array.from({ length: n }, () => "aaaaa").join(" ");
}

export function paraWords(
  id: string,
  n: number,
  footnoteRef?: string,
  refAtWord = 0,
): BlockNode {
  if (!footnoteRef) return { kind: "paragraph", id, spans: [{ text: words(n) }] };
  const ws = words(n).split(" ");
  const at = Math.min(refAtWord, ws.length - 1);
  return {
    kind: "paragraph",
    id,
    spans: [
      { text: ws.slice(0, at).join(" ") },
      { text: ws[at], footnoteRef },
      { text: ws.slice(at + 1).join(" ") },
    ].filter((s) => s.text.length > 0),
  };
}

export function heading(id: string): BlockNode {
  return { kind: "heading", id, text: "H", level: 1 };
}
