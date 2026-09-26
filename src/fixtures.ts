/**
 * 夹具：字形度量与节点树。核心布局计算只消费这些数据，不借浏览器。
 */

import type { GlyphMetrics, Node } from "./layout.ts";

export const glyphMetrics: GlyphMetrics = {
  fonts: {
    mono: {
      advance: {
        a: 8, b: 8, c: 8, d: 8, e: 8, i: 8, l: 8, n: 8, o: 8, r: 8, s: 8, t: 8,
        " ": 8, "0": 8, "1": 8, "2": 8, "3": 8, ":": 8, "/": 8,
      },
      defaultAdvance: 8,
      lineHeight: 20,
    },
    ui: {
      advance: {
        a: 9, b: 10, c: 9, d: 10, e: 9, i: 4, l: 4, n: 10, o: 10, r: 6, s: 8, t: 6,
        " ": 5, "0": 9, "1": 9, "2": 9, "3": 9, ":": 5, "/": 6,
      },
      defaultAdvance: 10,
      lineHeight: 22,
    },
  },
};

/** 对话框：标题 + 状态行 + 两个按钮。 */
export const dialogTree: Node = {
  kind: "box",
  id: "dialog",
  direction: "column",
  padding: 12,
  gap: 8,
  children: [
    { kind: "text", id: "title", text: "editor", font: "ui", size: 16 },
    { kind: "text", id: "status", text: "lines: 0", font: "mono", size: 16 },
    {
      kind: "box",
      id: "buttons",
      direction: "row",
      padding: 4,
      gap: 8,
      children: [
        { kind: "text", id: "btn-ok", text: "ok", font: "ui", size: 16 },
        { kind: "text", id: "btn-cancel", text: "cancel", font: "ui", size: 16 },
      ],
    },
  ],
};
