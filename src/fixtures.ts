import type { GlyphMetrics } from "./glyphs.ts";
import type { NodeSpec } from "./tree.ts";
import type { LayoutOptions } from "./layout.ts";

/**
 * 字形度量夹具:CJK 字符显式给宽,ASCII 走后备宽度。
 * 核心计算(文本测量、布局、命中)全部基于这些数据纯算。
 */
export const glyphMetrics: GlyphMetrics = {
  advances: {
    "设": 2, "置": 2, "面": 2, "板": 2,
    "确": 2, "认": 2, "操": 2, "作": 2,
    "帮": 2, "助": 2, "演": 2, "示": 2,
    "恢": 2, "复": 2, "按": 2, "钮": 2,
    "？": 2,
  },
  fallbackAdvance: 1,
  lineHeight: 4,
};

export const layoutOptions: LayoutOptions = {
  padding: 1,
  gap: 1,
  indent: 2,
};

/** 页面夹具:三个可聚焦按钮 + 一段 CJK 文本。 */
export function pageSpec(): NodeSpec {
  return {
    id: "app",
    children: [
      {
        id: "page",
        text: "Page",
        children: [
          { id: "btn-settings", text: "Open Settings", focusable: true },
          { id: "btn-help", text: "Open Help", focusable: true },
          { id: "btn-delete", text: "Delete Trigger", focusable: true },
          { id: "page-note", text: "设置面板演示" },
        ],
      },
    ],
  };
}

export function settingsLayerSpec(): NodeSpec {
  return {
    id: "layer-settings",
    text: "设置",
    children: [
      { id: "settings-title", text: "设置面板" },
      { id: "input-name", text: "Name", focusable: true, autofocus: true },
      { id: "btn-open-confirm", text: "Confirm", focusable: true },
      { id: "btn-close-settings", text: "Close", focusable: true },
    ],
  };
}

export function confirmLayerSpec(): NodeSpec {
  return {
    id: "layer-confirm",
    text: "确认",
    children: [
      { id: "confirm-label", text: "确认操作？" },
      { id: "btn-yes", text: "Yes", focusable: true, autofocus: true },
      { id: "btn-no", text: "No", focusable: true },
    ],
  };
}

export function helpLayerSpec(): NodeSpec {
  return {
    id: "layer-help",
    text: "帮助",
    children: [
      { id: "help-note", text: "帮助演示" },
      { id: "btn-close-help", text: "Close", focusable: true, autofocus: true },
    ],
  };
}
