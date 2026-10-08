import { visualColumn } from './indent';
import type { CstNode, RootNode } from './cst';
import { parse } from './parser';

export interface ModelEntry {
  line: number; // 1-based
  depth: number; // 0 = top level
  key: string;
  value: string | null; // null = block header
}

export interface ModelResult {
  entries: ModelEntry[];
  errorLines: number[];
}

/**
 * Reference model: an independent, line-oriented implementation built around
 * an explicit indentation stack. Used to cross-check the parser's hierarchy
 * and its error recovery (skip to the next top-level line).
 */
export function referenceModel(source: string): ModelResult {
  const rawLines = source === '' ? [] : source.split(/\r\n|\n/);
  if (rawLines.length > 0 && rawLines[rawLines.length - 1] === '') rawLines.pop();

  const entries: ModelEntry[] = [];
  const errorLines: number[] = [];
  const stack: Array<{ col: number; prefix: string }> = [{ col: 0, prefix: '' }];
  let prevHeader = false;

  let i = 0;
  while (i < rawLines.length) {
    const text = rawLines[i];
    const prefix = /^[ \t]*/.exec(text)![0];
    const body = text.slice(prefix.length);
    const trimmed = body.trim();
    if (trimmed === '' || trimmed.startsWith('#')) {
      i++;
      continue;
    }

    const col = visualColumn(prefix);
    let popped = false;
    while (stack.length > 1 && stack[stack.length - 1].col > col) {
      stack.pop();
      popped = true;
    }
    const top = stack[stack.length - 1];

    let ok = true;
    if (col > top.col) {
      if (prevHeader && !popped) stack.push({ col, prefix });
      else ok = false;
    } else if (top.prefix !== prefix) {
      ok = false;
    }

    const colon = body.indexOf(':');
    const key = colon < 0 ? '' : body.slice(0, colon).trim();
    if (ok && (colon < 0 || key === '')) ok = false;

    if (!ok) {
      errorLines.push(i + 1);
      i++;
      while (i < rawLines.length) {
        const t = rawLines[i];
        const p = /^[ \t]*/.exec(t)![0];
        const b = t.slice(p.length).trim();
        if (b !== '' && !b.startsWith('#') && visualColumn(p) === 0) break;
        i++;
      }
      stack.length = 1;
      prevHeader = false;
      continue;
    }

    const value = body.slice(colon + 1).trim();
    entries.push({ line: i + 1, depth: stack.length - 1, key, value: value === '' ? null : value });
    prevHeader = value === '';
    i++;
  }
  return { entries, errorLines };
}

export function offsetToLineCol(source: string, offset: number): { line: number; col: number } {
  let line = 1;
  let lineStart = 0;
  for (let i = 0; i < offset && i < source.length; i++) {
    if (source[i] === '\n') {
      line++;
      lineStart = i + 1;
    }
  }
  return { line, col: offset - lineStart + 1 };
}

/** Flatten a CST into the same shape the reference model produces. */
export function flatten(source: string, root: RootNode): ModelEntry[] {
  const out: ModelEntry[] = [];
  const walk = (nodes: CstNode[], depth: number) => {
    for (const node of nodes) {
      if (node.kind === 'leaf') {
        out.push({
          line: offsetToLineCol(source, node.start).line,
          depth,
          key: node.key,
          value: node.value,
        });
      } else if (node.kind === 'block') {
        out.push({
          line: offsetToLineCol(source, node.start).line,
          depth,
          key: node.key,
          value: null,
        });
        walk(node.children, depth + 1);
      }
    }
  };
  walk(root.children, 0);
  return out;
}

/** Cross-check the parser against the reference model. */
export function crossCheck(source: string): { model: ModelResult; actual: ModelEntry[]; errors: number[] } {
  const { root, errors } = parse(source);
  return {
    model: referenceModel(source),
    actual: flatten(source, root),
    errors: errors.map((e) => e.line),
  };
}
