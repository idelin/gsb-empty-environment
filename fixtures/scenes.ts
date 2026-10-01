import { LayoutTree } from '../src/tree.ts';
import { measureText } from './glyph-metrics.ts';

const FONT_SIZE = 14;
const LABEL_PADDING = 32;

function labelWidth(label: string): number {
  return measureText(label, FONT_SIZE) + LABEL_PADDING;
}

/**
 * Main scene: sidebar nav, a scrollable card list with a lazy (unmounted)
 * subtree, and a modal dialog whose confirm button is occluded by an overlay.
 * Node sizes for the nav come from fixture glyph metrics.
 */
export function buildMainScene(): LayoutTree {
  const tree = new LayoutTree();
  tree.setRect('root', { x: 0, y: 0, w: 800, h: 600 });

  tree.addNode({ id: 'sidebar', rect: { x: 0, y: 0, w: 200, h: 600 } });
  tree.addNode({ id: 'nav-home', parentId: 'sidebar', focusable: true, label: 'Home', rect: { x: 10, y: 10, w: labelWidth('Home'), h: 40 } });
  tree.addNode({ id: 'nav-list', parentId: 'sidebar', focusable: true, label: 'List', rect: { x: 10, y: 60, w: labelWidth('List'), h: 40 } });
  tree.addNode({ id: 'nav-about', parentId: 'sidebar', focusable: true, label: 'About', rect: { x: 10, y: 110, w: labelWidth('About'), h: 40 } });

  tree.addNode({ id: 'main', scrollable: true, rect: { x: 200, y: 0, w: 400, h: 600 } });
  tree.addNode({ id: 'card-1', parentId: 'main', focusable: true, rect: { x: 210, y: 10, w: 380, h: 100 } });
  tree.addNode({ id: 'card-2', parentId: 'main', focusable: true, rect: { x: 210, y: 120, w: 380, h: 100 } });
  tree.addNode({ id: 'card-3', parentId: 'main', focusable: true, rect: { x: 210, y: 230, w: 380, h: 100 } });
  tree.addNode({ id: 'card-4', parentId: 'main', focusable: true, rect: { x: 210, y: 700, w: 380, h: 100 } });
  tree.addNode({ id: 'lazy-host', parentId: 'main', mountState: 'unmounted', rect: { x: 210, y: 810, w: 380, h: 160 } });
  tree.addNode({ id: 'card-5', parentId: 'lazy-host', focusable: true, rect: { x: 210, y: 820, w: 380, h: 60 } });

  // Modal starts collapsed: its buttons are not eligible until it opens.
  tree.addNode({ id: 'dialog', modal: true, collapsed: true, rect: { x: 250, y: 150, w: 300, h: 300 } });
  tree.addNode({ id: 'dlg-secret', parentId: 'dialog', focusable: true, occludedBy: ['dlg-overlay'], rect: { x: 270, y: 320, w: 120, h: 40 } });
  tree.addNode({ id: 'dlg-overlay', parentId: 'dialog', rect: { x: 250, y: 300, w: 300, h: 60 } });
  tree.addNode({ id: 'dlg-ok', parentId: 'dialog', focusable: true, rect: { x: 270, y: 380, w: 120, h: 40 } });
  tree.addNode({ id: 'dlg-cancel', parentId: 'dialog', focusable: true, rect: { x: 410, y: 380, w: 120, h: 40 } });

  return tree;
}

/** Diagonal scene: exercises the public main-axis priority rule. */
export function buildDiagonalScene(): LayoutTree {
  const tree = new LayoutTree();
  tree.setRect('root', { x: 0, y: 0, w: 1000, h: 1000 });
  tree.addNode({ id: 'origin', focusable: true, rect: { x: 100, y: 100, w: 50, h: 50 } });
  // Diagonal: small main-axis step (40) with a moderate cross-axis offset (60).
  tree.addNode({ id: 'diag-near', focusable: true, rect: { x: 140, y: 160, w: 50, h: 50 } });
  // On-axis but much farther on the main axis (200).
  tree.addNode({ id: 'axis-far', focusable: true, rect: { x: 300, y: 100, w: 50, h: 50 } });
  return tree;
}

/** Horizontal strip for wrap-around tests. */
export function buildWrapScene(): LayoutTree {
  const tree = new LayoutTree();
  tree.setRect('root', { x: 0, y: 0, w: 300, h: 60 });
  tree.addNode({ id: 'b1', focusable: true, rect: { x: 0, y: 0, w: 50, h: 50 } });
  tree.addNode({ id: 'b2', focusable: true, rect: { x: 100, y: 0, w: 50, h: 50 } });
  tree.addNode({ id: 'b3', focusable: true, rect: { x: 200, y: 0, w: 50, h: 50 } });
  return tree;
}

/** Vertical list for the legal-fallback (wrap disabled) tests. */
export function buildColumnScene(): LayoutTree {
  const tree = new LayoutTree();
  tree.setRect('root', { x: 0, y: 0, w: 60, h: 300 });
  tree.addNode({ id: 'c1', focusable: true, rect: { x: 0, y: 0, w: 50, h: 50 } });
  tree.addNode({ id: 'c2', focusable: true, rect: { x: 0, y: 100, w: 50, h: 50 } });
  tree.addNode({ id: 'c3', focusable: true, rect: { x: 0, y: 200, w: 50, h: 50 } });
  return tree;
}

/** Nested scrollers: an inner list inside an outer page scroller. */
export function buildNestedScrollScene(): LayoutTree {
  const tree = new LayoutTree();
  tree.setRect('root', { x: 0, y: 0, w: 800, h: 600 });
  tree.addNode({ id: 'page', scrollable: true, rect: { x: 0, y: 0, w: 800, h: 600 } });
  tree.addNode({ id: 'header', parentId: 'page', focusable: true, rect: { x: 0, y: 0, w: 800, h: 60 } });
  tree.addNode({ id: 'list', parentId: 'page', scrollable: true, rect: { x: 100, y: 700, w: 300, h: 200 } });
  tree.addNode({ id: 'item-a', parentId: 'list', focusable: true, rect: { x: 110, y: 710, w: 280, h: 40 } });
  tree.addNode({ id: 'item-b', parentId: 'list', focusable: true, rect: { x: 110, y: 760, w: 280, h: 40 } });
  tree.addNode({ id: 'item-f', parentId: 'list', focusable: true, rect: { x: 110, y: 1010, w: 280, h: 40 } });
  return tree;
}
