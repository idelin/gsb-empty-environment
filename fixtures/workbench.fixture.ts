/**
 * 夹具：节点树与盒/字形度量。
 * 所有核心计算（可见性、可聚焦性）都从这些数据推导，不借浏览器布局引擎。
 */

import { NodeTree } from '../src/tree.ts';
import type { GlyphMetric, NodeMetrics, NodeSpec } from '../src/tree.ts';
import { LayerManager } from '../src/layer-manager.ts';

/** 为文本生成逐字字形度量：CJK 全宽 14px，其余 8px */
export function glyphsFor(text: string): GlyphMetric[] {
  return [...text].map((ch) => ({
    advance: /[⺀-﫿＀-￯　-〿]/.test(ch) ? 14 : 8,
  }));
}

export function screenSpec(): NodeSpec {
  return {
    id: 'screen',
    children: [
      {
        id: 'app-root',
        children: [
          {
            id: 'header',
            children: [{ id: 'btn-nav-home', focusable: true, text: '首页' }],
          },
          {
            id: 'main',
            children: [
              { id: 'btn-open-a', focusable: true, text: '打开弹层A' },
              { id: 'btn-open-b', focusable: true, text: '打开弹层B' },
              { id: 'txt-hero', text: '你好，世界' },
              { id: 'btn-ghost', focusable: true, text: '隐形按钮' },
              {
                id: 'sec-collapsed',
                children: [{ id: 'btn-hidden', focusable: true, text: '隐藏按钮' }],
              },
            ],
          },
          {
            id: 'footer',
            children: [{ id: 'btn-fallback', focusable: true, text: '后备焦点' }],
          },
        ],
      },
      { id: 'layer-host' },
    ],
  };
}

export function baseMetrics(): Record<string, NodeMetrics> {
  return {
    screen: { width: 1280, height: 800 },
    'app-root': { width: 1280, height: 800 },
    header: { width: 1280, height: 56 },
    'btn-nav-home': { width: 64, height: 32 },
    main: { width: 1280, height: 688 },
    'btn-open-a': { width: 120, height: 36 },
    'btn-open-b': { width: 120, height: 36 },
    'txt-hero': { glyphs: glyphsFor('你好，世界'), lineHeight: 24 },
    'btn-ghost': { width: 0, height: 0 }, // 盒塌陷：不可见
    'sec-collapsed': { display: 'none' }, // 整段隐藏
    'btn-hidden': { width: 96, height: 32 },
    footer: { width: 1280, height: 56 },
    'btn-fallback': { width: 96, height: 32 },
    'layer-host': { width: 1280, height: 800 },
  };
}

export function layerASpec(rootId = 'layer-a'): NodeSpec {
  return {
    id: rootId,
    children: [
      { id: `${rootId}-title`, text: '弹层A' },
      { id: `${rootId}-open-b`, focusable: true, text: '打开B' },
      { id: `${rootId}-close`, focusable: true, text: '关闭A' },
    ],
  };
}

export function layerAMetrics(rootId = 'layer-a'): Record<string, NodeMetrics> {
  return {
    [rootId]: { width: 480, height: 320 },
    [`${rootId}-title`]: { glyphs: glyphsFor('弹层A'), lineHeight: 24 },
    [`${rootId}-open-b`]: { width: 88, height: 32 },
    [`${rootId}-close`]: { width: 88, height: 32 },
  };
}

export function layerBSpec(rootId = 'layer-b'): NodeSpec {
  return {
    id: rootId,
    children: [
      { id: `${rootId}-title`, text: '弹层B' },
      { id: `${rootId}-close`, focusable: true, text: '关闭B' },
      { id: `${rootId}-noop`, focusable: true, text: '空操作' },
    ],
  };
}

export function layerBMetrics(rootId = 'layer-b'): Record<string, NodeMetrics> {
  return {
    [rootId]: { width: 360, height: 240 },
    [`${rootId}-title`]: { glyphs: glyphsFor('弹层B'), lineHeight: 24 },
    [`${rootId}-close`]: { width: 88, height: 32 },
    [`${rootId}-noop`]: { width: 88, height: 32 },
  };
}

export interface World {
  tree: NodeTree;
  manager: LayerManager;
  mountLayerA(rootId?: string): void;
  mountLayerB(rootId?: string): void;
}

export function buildWorld(): World {
  const tree = new NodeTree(screenSpec(), baseMetrics());
  const manager = new LayerManager(tree, { fallbackId: 'btn-fallback' });
  return {
    tree,
    manager,
    mountLayerA(rootId = 'layer-a') {
      tree.mount('layer-host', layerASpec(rootId), layerAMetrics(rootId));
    },
    mountLayerB(rootId = 'layer-b') {
      tree.mount('layer-host', layerBSpec(rootId), layerBMetrics(rootId));
    },
  };
}
