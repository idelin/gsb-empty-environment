// Concrete spec: a tiny multi-paragraph editor whose layout is computed by the
// pure layout core (fixtures supply the node tree and glyph metrics).

import {
  findNode,
  glyphMetrics,
  makeDocumentFixture,
  replaceNodeText,
  type NodeTree,
} from './fixtures.ts';
import { layoutDocument, type DocumentLayout } from './layout.ts';
import {
  modeExists,
  type Event,
  type ModeNode,
  type Spec,
} from './spec.ts';

export interface EditorState {
  readonly mode: string; // hierarchical path, e.g. 'active.insert'
  readonly tree: NodeTree;
  readonly focusId: string;
  readonly cursor: number;
  readonly anchor: number | null;
  readonly maxWidth: number;
  readonly layout: DocumentLayout;
}

export const modeRoot: ModeNode = {
  name: 'app',
  children: [
    {
      name: 'active',
      children: [{ name: 'insert' }, { name: 'select' }],
    },
    { name: 'readonly' },
    { name: 'dialog', children: [{ name: 'confirm' }] },
  ],
};

const focusedText = (state: EditorState): string => {
  const node = findNode(state.tree, state.focusId);
  return node?.text ?? '';
};

const relayout = (tree: NodeTree, maxWidth: number): DocumentLayout =>
  layoutDocument(glyphMetrics, tree, maxWidth);

const initial = (): EditorState => {
  const tree = makeDocumentFixture();
  const maxWidth = 120;
  return {
    mode: 'active.insert',
    tree,
    focusId: 'p1',
    cursor: 0,
    anchor: null,
    maxWidth,
    layout: relayout(tree, maxWidth),
  };
};

const knownGlyph = (ch: string): boolean =>
  ch === ' ' || glyphMetrics.advance[ch] !== undefined;

export const editorSpec: Spec<EditorState> = {
  name: 'editor-v1',
  modeRoot,
  initial,
  modeOf: (s) => s.mode,
  resolveTarget: (state, event) => {
    const payload = event.payload as { target?: string } | undefined;
    if (payload?.target === undefined) return null;
    const node = findNode(state.tree, payload.target);
    if (!node) return `target node '${payload.target}' not found in tree`;
    if (node.kind !== 'paragraph') return `target node '${payload.target}' is not a paragraph`;
    return null;
  },
  events: [
    {
      name: 'FOCUS',
      modes: ['active.*'],
      action: (s, e) => {
        const { target } = e.payload as { target: string };
        const len = findNode(s.tree, target)?.text?.length ?? 0;
        return { ...s, focusId: target, cursor: Math.min(s.cursor, len), anchor: null };
      },
      domain: () => [{ target: 'p1' }, { target: 'p2' }, { target: 'p3' }],
    },
    {
      name: 'TYPE',
      modes: ['active.insert'],
      guard: (_s, e) => {
        const { ch } = e.payload as { ch: string };
        return knownGlyph(ch) ? null : `no glyph metric for '${ch}'`;
      },
      action: (s, e) => {
        // atomic multi-field update: tree text, cursor and layout move together
        const { ch } = e.payload as { ch: string };
        const text = focusedText(s);
        const nextText = text.slice(0, s.cursor) + ch + text.slice(s.cursor);
        const tree = replaceNodeText(s.tree, s.focusId, nextText);
        return { ...s, tree, cursor: s.cursor + 1, anchor: null, layout: relayout(tree, s.maxWidth) };
      },
      domain: () => [{ ch: 'a' }, { ch: 'b' }, { ch: ' ' }],
    },
    {
      name: 'BACKSPACE',
      modes: ['active.insert'],
      guard: (s) => (s.cursor > 0 ? null : 'cursor at start of paragraph'),
      action: (s) => {
        const text = focusedText(s);
        const nextText = text.slice(0, s.cursor - 1) + text.slice(s.cursor);
        const tree = replaceNodeText(s.tree, s.focusId, nextText);
        return { ...s, tree, cursor: s.cursor - 1, layout: relayout(tree, s.maxWidth) };
      },
      domain: () => [undefined],
    },
    {
      name: 'MOVE',
      modes: ['active.*'],
      guard: (s, e) => {
        const { to } = e.payload as { to: number };
        return to >= 0 && to <= focusedText(s).length
          ? null
          : `offset ${to} out of bounds 0..${focusedText(s).length}`;
      },
      action: (s, e) => ({ ...s, cursor: (e.payload as { to: number }).to, anchor: null }),
      domain: () => [{ to: 0 }, { to: 1 }, { to: 5 }],
    },
    {
      name: 'SELECT',
      modes: ['active.select'],
      guard: (s, e) => {
        const { to } = e.payload as { to: number };
        return to >= 0 && to <= focusedText(s).length
          ? null
          : `offset ${to} out of bounds 0..${focusedText(s).length}`;
      },
      action: (s, e) => ({ ...s, anchor: s.cursor, cursor: (e.payload as { to: number }).to }),
      domain: () => [{ to: 0 }, { to: 2 }],
    },
    {
      name: 'RESIZE',
      modes: ['**'],
      guard: (_s, e) => {
        const { width } = e.payload as { width: number };
        return width >= 20 ? null : `width ${width} below minimum 20`;
      },
      action: (s, e) => {
        const maxWidth = (e.payload as { width: number }).width;
        return { ...s, maxWidth, layout: relayout(s.tree, maxWidth) };
      },
      domain: () => [{ width: 40 }, { width: 120 }],
    },
    {
      name: 'ENTER',
      modes: ['**'],
      guard: (_s, e) => {
        const { mode } = e.payload as { mode: string };
        return modeExists(modeRoot, mode) ? null : `unknown mode '${mode}'`;
      },
      action: (s, e) => {
        // entering a dialog atomically drops any selection
        const mode = (e.payload as { mode: string }).mode;
        return mode.startsWith('dialog') ? { ...s, mode, anchor: null } : { ...s, mode };
      },
      domain: () => [
        { mode: 'active.insert' },
        { mode: 'active.select' },
        { mode: 'readonly' },
        { mode: 'dialog.confirm' },
      ],
    },
    {
      name: 'CONFIRM',
      modes: ['dialog.confirm'],
      action: (s) => ({ ...s, mode: 'active.insert' }),
      domain: () => [undefined],
    },
  ],
  invariants: [
    {
      name: 'cursor-in-bounds',
      check: (s) =>
        s.cursor >= 0 && s.cursor <= focusedText(s).length
          ? null
          : `cursor ${s.cursor} outside 0..${focusedText(s).length}`,
    },
    {
      name: 'anchor-valid',
      check: (s) =>
        s.anchor === null || (s.anchor >= 0 && s.anchor <= focusedText(s).length)
          ? null
          : `anchor ${s.anchor} invalid`,
    },
    {
      name: 'mode-known',
      check: (s) => (modeExists(modeRoot, s.mode) ? null : `mode '${s.mode}' not in mode tree`),
    },
    {
      name: 'glyph-covered',
      check: (s) => {
        const walk = (n: NodeTree): string | null => {
          for (const ch of n.text ?? '') {
            if (!knownGlyph(ch)) return `no glyph metric for '${ch}' in node '${n.id}'`;
          }
          for (const c of n.children ?? []) {
            const bad = walk(c);
            if (bad) return bad;
          }
          return null;
        };
        return walk(s.tree);
      },
    },
    {
      name: 'layout-consistent',
      check: (s) => {
        const expected = relayout(s.tree, s.maxWidth);
        return JSON.stringify(expected) === JSON.stringify(s.layout)
          ? null
          : 'cached layout diverges from pure recomputation';
      },
    },
  ],
  unreachable: [
    {
      name: 'dialog-with-selection',
      matches: (s) => s.mode.startsWith('dialog') && s.anchor !== null,
    },
  ],
};
