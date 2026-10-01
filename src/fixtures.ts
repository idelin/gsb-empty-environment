/**
 * 夹具：节点树与字形度量。
 * 所有布局坐标在此用纯数据 + 自算文本宽度得出，核心计算不借浏览器布局引擎。
 */

import { FocusTree } from './tree.ts';
import type { NodeInit } from './tree.ts';
import { measureText } from './metrics.ts';
import type { GlyphMetrics } from './metrics.ts';
import { Scheduler } from './scheduler.ts';
import { FocusNavigator } from './navigator.ts';

/** 字形度量夹具：仅收录用得到的字符。 */
export const glyphMetrics: GlyphMetrics = {
  advance: { F: 7, i: 3, l: 3, e: 6, E: 7, d: 6, t: 4, V: 8, w: 9 },
  fallback: 6,
};

const TOOLBAR_PAD = 8;
const TOOLBAR_GAP = 8;

function toolbarButton(id: string, label: string, x: number): { init: NodeInit; width: number } {
  const width = measureText(label, glyphMetrics) + TOOLBAR_PAD * 2;
  return {
    init: { id, label, rect: { x, y: 8, w: width, h: 24 }, focusable: true },
    width,
  };
}

function buildToolbar(): NodeInit {
  const labels: Array<[string, string]> = [
    ['BTN-FILE', 'File'],
    ['BTN-EDIT', 'Edit'],
    ['BTN-VIEW', 'View'],
  ];
  let x = TOOLBAR_PAD;
  const children: NodeInit[] = [];
  for (const [id, label] of labels) {
    const { init, width } = toolbarButton(id, label, x);
    children.push(init);
    x += width + TOOLBAR_GAP;
  }
  return { id: 'TOOLBAR', rect: { x: 0, y: 0, w: 800, h: 40 }, orientation: 'horizontal', children };
}

function buildSidebar(): NodeInit {
  const children: NodeInit[] = [];
  for (let i = 0; i < 8; i++) {
    const id = `S${i + 1}`;
    children.push({
      id,
      label: `Side ${i + 1}`,
      rect: { x: 8, y: 40 + i * 48, w: 184, h: 36 },
      focusable: true,
      // S7：慢异步挂载（100ms），用于验证令牌过期与完成时校验。
      ...(id === 'S7' ? { mounted: false, mountDelay: 100 } : {}),
    });
  }
  return {
    id: 'SIDEBAR',
    rect: { x: 0, y: 40, w: 200, h: 360 },
    scroll: { viewport: { x: 0, y: 40, w: 200, h: 360 }, contentSize: { w: 200, h: 400 } },
    children,
  };
}

function buildMain(): NodeInit {
  const chips: NodeInit[] = [];
  for (let i = 0; i < 6; i++) {
    chips.push({
      id: `H${i + 1}`,
      label: `Chip ${i + 1}`,
      rect: { x: 216 + i * 100, y: 284, w: 76, h: 28 },
      focusable: true,
    });
  }
  return {
    id: 'MAIN',
    rect: { x: 200, y: 40, w: 600, h: 360 },
    children: [
      // 主区节点中心与侧栏 48px 网格对齐：公开主轴优先规则下，
      // 侧栏纵向移动不会被斜向候选截获（主轴位移平局时副轴偏移裁决）。
      { id: 'C1', label: 'Card 1', rect: { x: 208, y: 76, w: 180, h: 60 }, focusable: true },
      { id: 'C2', label: 'Card 2', rect: { x: 208, y: 172, w: 180, h: 60 }, focusable: true },
      {
        id: 'C3',
        label: 'Card 3',
        rect: { x: 208, y: 268, w: 400, h: 60 },
        focusable: true,
        children: [
          {
            id: 'CHIPS',
            rect: { x: 216, y: 280, w: 300, h: 36 },
            scroll: { viewport: { x: 216, y: 280, w: 300, h: 36 }, contentSize: { w: 576, h: 36 } },
            children: chips,
          },
        ],
      },
      {
        id: 'ADV',
        rect: { x: 208, y: 324, w: 400, h: 84 },
        children: [
          { id: 'A1', label: 'Adv 1', rect: { x: 216, y: 332, w: 120, h: 28 }, focusable: true },
          { id: 'A2', label: 'Adv 2', rect: { x: 216, y: 380, w: 120, h: 28 }, focusable: true },
        ],
      },
    ],
  };
}

function buildModal(): NodeInit {
  return {
    id: 'MODAL',
    rect: { x: 0, y: 0, w: 800, h: 600 },
    modal: true,
    visible: false,
    z: 101,
    children: [
      { id: 'MASK', rect: { x: 0, y: 0, w: 800, h: 600 }, opaque: true, z: 100 },
      {
        id: 'DIALOG',
        rect: { x: 250, y: 150, w: 300, h: 240 },
        children: [
          { id: 'M-CONFIRM', label: 'Confirm', rect: { x: 270, y: 180, w: 120, h: 32 }, focusable: true },
          { id: 'M-CANCEL', label: 'Cancel', rect: { x: 410, y: 180, w: 120, h: 32 }, focusable: true },
          { id: 'M-L1', label: 'Row 1', rect: { x: 270, y: 230, w: 120, h: 28 }, focusable: true },
          // M-L2：异步挂载（50ms）。
          { id: 'M-L2', label: 'Row 2', rect: { x: 270, y: 262, w: 120, h: 28 }, focusable: true, mounted: false, mountDelay: 50 },
          { id: 'M-L3', label: 'Row 3', rect: { x: 270, y: 294, w: 120, h: 28 }, focusable: true },
        ],
      },
    ],
  };
}

export function buildAppTree(): FocusTree {
  return new FocusTree({
    id: 'ROOT',
    rect: { x: 0, y: 0, w: 800, h: 600 },
    children: [
      buildToolbar(),
      buildSidebar(),
      buildMain(),
      {
        id: 'FOOTER',
        rect: { x: 0, y: 400, w: 800, h: 48 },
        children: [
          { id: 'OPEN', label: 'Open Modal', rect: { x: 8, y: 408, w: 120, h: 32 }, focusable: true },
        ],
      },
      buildModal(),
    ],
  });
}

export interface App {
  tree: FocusTree;
  scheduler: Scheduler;
  navigator: FocusNavigator;
}

export function createApp(): App {
  const tree = buildAppTree();
  const scheduler = new Scheduler();
  const navigator = new FocusNavigator(tree, scheduler, 'BTN-FILE');
  return { tree, scheduler, navigator };
}
