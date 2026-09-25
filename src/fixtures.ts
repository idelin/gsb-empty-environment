import type { FontFace, LayoutNode, TextStyle } from "./types.ts";

/**
 * Fixture font metrics. All glyph advances are given data — no browser layout
 * engine is involved anywhere in the core computation.
 */

function advancesFromString(chars: string, advance: number): Record<string, number> {
  const out: Record<string, number> = {};
  for (const ch of chars) out[ch] = advance;
  return out;
}

export const SERIF_V1: FontFace = {
  family: "Serif",
  version: 1,
  unitsPerEm: 1000,
  ascent: 800,
  descent: 200,
  lineGap: 200,
  glyphAdvances: {
    ...advancesFromString("abcdefgh", 500),
    ...advancesFromString("ijkl", 250),
    " ": 250,
    ".": 250,
  },
  defaultAdvance: 500,
};

/** Hot-reloaded Serif: same family, wider glyphs, bumped version. */
export const SERIF_V2: FontFace = {
  ...SERIF_V1,
  version: 2,
  glyphAdvances: {
    ...advancesFromString("abcdefgh", 600),
    ...advancesFromString("ijkl", 300),
    " ": 300,
    ".": 300,
  },
  defaultAdvance: 600,
};

export const MONO_V1: FontFace = {
  family: "Mono",
  version: 1,
  unitsPerEm: 1000,
  ascent: 750,
  descent: 250,
  lineGap: 0,
  glyphAdvances: advancesFromString("xyz01 ", 600),
  defaultAdvance: 600,
};

/** Coarse metrics used only for the estimate channel before fonts arrive. */
export const ESTIMATE_FACE: FontFace = {
  family: "__estimate__",
  version: 0,
  unitsPerEm: 1000,
  ascent: 800,
  descent: 200,
  lineGap: 200,
  glyphAdvances: {},
  defaultAdvance: 600,
};

export const STYLE_A: TextStyle = { fontFamily: "Serif", fontSize: 16, weight: 400, letterSpacing: 0 };
export const STYLE_A_SPACED: TextStyle = { ...STYLE_A, letterSpacing: 2 };
export const STYLE_B: TextStyle = { fontFamily: "Mono", fontSize: 14, weight: 400, letterSpacing: 0 };

/** Fixture node tree: a 120px box with three text runs. */
export function makeNodeTree(): LayoutNode {
  return {
    kind: "box",
    id: "root",
    width: 120,
    children: [
      { kind: "text", id: "t1", text: "abcd efgh", style: STYLE_A },
      { kind: "text", id: "t2", text: "ijkl", style: STYLE_A_SPACED },
      {
        kind: "box",
        id: "nested",
        width: 60,
        children: [{ kind: "text", id: "t3", text: "xyz 01", style: STYLE_B }],
      },
    ],
  };
}
