import type { GlyphMetrics } from "../src/metrics.ts";

/**
 * Fixture glyph metrics (font units, unitsPerEm = 1000).
 * Stands in for real font metrics; core measurement never touches a browser.
 */
export const fixtureGlyphMetrics: GlyphMetrics = {
  unitsPerEm: 1000,
  defaultAdvance: 500,
  advances: {
    " ": 250,
    ".": 250,
    "%": 700,
    "…": 800,
    "0": 500, "1": 500, "2": 500, "3": 500, "4": 500,
    "5": 500, "6": 500, "7": 500, "8": 500, "9": 500,
    a: 450, b: 480, c: 430, d: 480, e: 450, f: 280, g: 480, h: 490,
    i: 220, j: 220, k: 440, l: 220, m: 720, n: 490, o: 470, p: 480,
    q: 480, r: 300, s: 400, t: 280, u: 490, v: 450, w: 680, x: 450,
    y: 450, z: 420,
    A: 600, B: 580, C: 620, D: 640, E: 560, F: 540, G: 660, H: 660,
    I: 240, L: 520, M: 800, O: 680, P: 560, R: 600, S: 560, T: 560,
    U: 660, W: 900,
    上: 1000, 传: 1000, 完: 1000, 成: 1000, 下: 1000, 载: 1000,
    进: 1000, 度: 1000, 更: 1000, 新: 1000, 可: 1000, 用: 1000,
    失: 1000, 败: 1000, 已: 1000, 取: 1000, 消: 1000,
  },
};
