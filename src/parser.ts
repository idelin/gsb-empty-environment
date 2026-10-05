/**
 * Block-tree builder with hash-consing.
 *
 * The tree is purely structural: nodes carry no absolute positions, so an
 * unchanged subtree keeps an identical content hash (`id`) no matter where
 * it sits in the document. A shared NodeCache makes identical subtrees —
 * across edits and across locations (e.g. a pasted copy of a block) — come
 * out as the *same object*, which is what makes node ids stable and subtree
 * reuse provable by reference equality.
 *
 * Positions live in a separate per-version occurrence list, so shifting
 * lines (insert a blank line above) never invalidates any node.
 *
 * Error recovery: tab indentation, inconsistent dedents, unmatched closing
 * brackets and unclosed brackets are recorded on the offending node; the
 * parser always keeps going, so an error never swallows the rest of file.
 */

import type { LexedLine } from './lexer.ts';

export type ErrorKind = 'tab' | 'bracket' | 'dedent' | 'unclosed';

export interface NodeError {
  /** Line offset relative to the node's head line. */
  offset: number;
  kind: ErrorKind;
  message: string;
}

export interface Continuation {
  /** Line offset relative to the node's head line. */
  offset: number;
  text: string;
}

export interface SyntaxNode {
  /** Content hash; stable as long as the subtree is unchanged. */
  readonly id: string;
  readonly text: string;
  readonly indent: number;
  readonly continuations: readonly Continuation[];
  readonly errors: readonly NodeError[];
  readonly children: readonly SyntaxNode[];
}

/** One placement of a node in a specific document version. */
export interface Occurrence {
  node: SyntaxNode;
  startLine: number;
  endLine: number;
  depth: number;
}

export interface DocumentError {
  line: number;
  kind: ErrorKind;
  message: string;
  nodeId: string;
}

export interface BuiltTree {
  root: SyntaxNode;
  occurrences: Occurrence[];
  errors: DocumentError[];
}

export type NodeCache = Map<string, SyntaxNode>;

function hashString(input: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, '0');
}

interface Frame {
  indent: number;
  text: string;
  headLine: number;
  depth: number;
  continuations: Continuation[];
  errors: NodeError[];
  children: SyntaxNode[];
  lastLine: number;
}

export function buildTree(lexed: LexedLine[], cache: NodeCache): BuiltTree {
  const occurrences: Occurrence[] = [];
  const rootFrame: Frame = {
    indent: -1, text: '', headLine: 0, depth: -1,
    continuations: [], errors: [], children: [], lastLine: 0,
  };
  const stack: Frame[] = [rootFrame];
  let openFrame: Frame | null = null;
  let prevIndent = 0;

  const intern = (frame: Frame): SyntaxNode => {
    const key = JSON.stringify([
      frame.text, frame.indent, frame.continuations, frame.errors,
      frame.children.map((child) => child.id),
    ]);
    const id = `n_${hashString(key)}`;
    const cached = cache.get(id);
    if (cached) return cached;
    const node: SyntaxNode = {
      id,
      text: frame.text,
      indent: frame.indent,
      continuations: frame.continuations,
      errors: frame.errors,
      children: frame.children,
    };
    cache.set(id, node);
    return node;
  };

  const closeTopFrame = (): void => {
    const frame = stack.pop()!;
    const node = intern(frame);
    const parent = stack[stack.length - 1];
    parent.children.push(node);
    parent.lastLine = Math.max(parent.lastLine, frame.lastLine);
    occurrences.push({ node, startLine: frame.headLine, endLine: frame.lastLine, depth: frame.depth });
  };

  for (let i = 0; i < lexed.length; i += 1) {
    const line = lexed[i];
    if (line.kind !== 'code') continue; // blank / comment: trivia

    if (line.depthIn > 0) {
      // Continuation line: indentation is irrelevant inside brackets.
      const target = openFrame;
      if (target) {
        const offset = i - target.headLine;
        target.continuations.push({ offset, text: line.text });
        if (line.tabError) {
          target.errors.push({ offset, kind: 'tab', message: 'tab character in indentation' });
        }
        if (line.bracketError) {
          target.errors.push({ offset, kind: 'bracket', message: 'unmatched closing bracket' });
        }
        target.lastLine = Math.max(target.lastLine, i);
        if (line.depthOut === 0) openFrame = null;
      }
      continue;
    }

    // Structural line.
    const indent = line.indent;
    const errors: NodeError[] = [];
    if (line.tabError) {
      errors.push({ offset: 0, kind: 'tab', message: 'tab character in indentation' });
    }
    if (line.bracketError) {
      errors.push({ offset: 0, kind: 'bracket', message: 'unmatched closing bracket' });
    }
    if (indent < prevIndent && !stack.some((frame) => frame.indent === indent)) {
      errors.push({ offset: 0, kind: 'dedent', message: `inconsistent dedent to column ${indent}` });
    }
    while (stack[stack.length - 1].indent >= indent) {
      closeTopFrame();
    }
    const frame: Frame = {
      indent, text: line.text, headLine: i, depth: stack.length - 1,
      continuations: [], errors, children: [], lastLine: i,
    };
    stack.push(frame);
    if (line.depthOut > 0) openFrame = frame;
    prevIndent = indent;
  }

  if (openFrame) {
    openFrame.errors.push({ offset: 0, kind: 'unclosed', message: 'bracket opened here is never closed' });
  }
  while (stack.length > 1) closeTopFrame();

  const rootId = `root_${hashString(JSON.stringify(rootFrame.children.map((child) => child.id)))}`;
  let root = cache.get(rootId);
  if (!root) {
    root = { id: rootId, text: '', indent: -1, continuations: [], errors: [], children: rootFrame.children };
    cache.set(rootId, root);
  }

  occurrences.sort((a, b) => a.startLine - b.startLine);
  const errors: DocumentError[] = [];
  for (const occ of occurrences) {
    for (const err of occ.node.errors) {
      errors.push({ line: occ.startLine + err.offset, kind: err.kind, message: err.message, nodeId: occ.node.id });
    }
  }
  errors.sort((a, b) => a.line - b.line);
  return { root, occurrences, errors };
}
