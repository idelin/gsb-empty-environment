import { visualColumn, firstDiffIndex } from './indent';
import type { BlockNode, CstNode, LeafNode, RootNode, TriviaNode } from './cst';

export type ErrorCode =
  | 'ambiguous-indent'
  | 'bad-dedent'
  | 'unexpected-indent'
  | 'bad-line';

export interface Diagnostic {
  code: ErrorCode;
  message: string;
  line: number; // 1-based
  col: number; // 1-based, visual column
  start: number; // byte offset into the source
  end: number;
}

export interface ParseResult {
  root: RootNode;
  errors: Diagnostic[];
}

interface Line {
  number: number; // 1-based
  start: number;
  contentStart: number; // after the indent prefix
  textEnd: number; // before the line terminator
  end: number; // after the line terminator
  indent: string;
  body: string; // text after indent, terminator excluded
}

export function splitLines(source: string): Line[] {
  const lines: Line[] = [];
  let start = 0;
  let number = 1;
  while (start < source.length) {
    let textEnd = start;
    while (textEnd < source.length && source[textEnd] !== '\n' && source[textEnd] !== '\r') {
      textEnd++;
    }
    let end = textEnd;
    if (source[end] === '\r' && source[end + 1] === '\n') end += 2;
    else if (end < source.length) end += 1;
    let contentStart = start;
    while (
      contentStart < textEnd &&
      (source[contentStart] === ' ' || source[contentStart] === '\t')
    ) {
      contentStart++;
    }
    lines.push({
      number: number++,
      start,
      contentStart,
      textEnd,
      end,
      indent: source.slice(start, contentStart),
      body: source.slice(contentStart, textEnd),
    });
    start = end;
  }
  return lines;
}

function isTrivia(line: Line): boolean {
  const trimmed = line.body.trim();
  return trimmed === '' || trimmed.startsWith('#');
}

interface Item {
  key: string;
  keyStart: number;
  keyEnd: number;
  hasValue: boolean;
  value: string;
  valueStart: number;
  valueEnd: number;
}

function parseItem(line: Line): Item | null {
  const colon = line.body.indexOf(':');
  if (colon < 0) return null;
  const rawKey = line.body.slice(0, colon);
  const key = rawKey.trim();
  if (key === '') return null;
  const keyStart = line.contentStart + (rawKey.length - rawKey.trimStart().length);
  const keyEnd = keyStart + key.length;
  const rest = line.body.slice(colon + 1);
  const value = rest.trim();
  const valueStart = line.contentStart + colon + 1 + (rest.length - rest.trimStart().length);
  const valueEnd = valueStart + value.length;
  return { key, keyStart, keyEnd, hasValue: value !== '', value, valueStart, valueEnd };
}

interface Level {
  col: number;
  prefix: string;
  block: BlockNode;
}

export function parse(source: string): ParseResult {
  const lines = splitLines(source);
  const root: RootNode = { kind: 'root', start: 0, end: source.length, children: [] };
  const errors: Diagnostic[] = [];
  const stack: Level[] = []; // root is implicit level { col: 0, prefix: '' }
  let pending: BlockNode | null = null; // header of the previous significant line

  const container = (): RootNode | BlockNode =>
    stack.length > 0 ? stack[stack.length - 1].block : root;
  const topCol = (): number => (stack.length > 0 ? stack[stack.length - 1].col : 0);
  const topPrefix = (): string => (stack.length > 0 ? stack[stack.length - 1].prefix : '');

  const closeLevel = () => {
    const level = stack.pop()!;
    const children = level.block.children;
    level.block.end = children.length > 0 ? children[children.length - 1].end : level.block.headerEnd;
  };

  const trivia = (line: Line): TriviaNode => ({ kind: 'trivia', start: line.start, end: line.end });

  const report = (code: ErrorCode, line: Line, col: number, start: number, end: number, message: string) => {
    errors.push({ code, message, line: line.number, col, start, end });
  };

  /** Error recovery: drop the offending line and everything until the next
   *  significant top-level line; skipped text stays in the tree as trivia. */
  const recover = (from: number): number => {
    while (stack.length > 0) closeLevel();
    pending = null;
    root.children.push(trivia(lines[from]));
    let j = from + 1;
    while (j < lines.length) {
      const line = lines[j];
      if (!isTrivia(line) && visualColumn(line.indent) === 0) break;
      root.children.push(trivia(line));
      j++;
    }
    return j;
  };

  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (isTrivia(line)) {
      (pending ?? container()).children.push(trivia(line));
      i++;
      continue;
    }

    const col = visualColumn(line.indent);
    let popped = false;
    while (stack.length > 0 && topCol() > col) {
      closeLevel();
      popped = true;
    }

    if (col > topCol()) {
      if (pending && !popped) {
        stack.push({ col, prefix: line.indent, block: pending });
      } else {
        const code: ErrorCode = popped ? 'bad-dedent' : 'unexpected-indent';
        report(
          code,
          line,
          1,
          line.start,
          line.contentStart,
          popped
            ? `dedent to column ${col} matches no outer level`
            : `unexpected indent to column ${col}`,
        );
        i = recover(i);
        continue;
      }
    } else if (topPrefix() !== line.indent) {
      const diff = firstDiffIndex(topPrefix(), line.indent);
      const badCol = visualColumn(line.indent.slice(0, diff)) + 1;
      report(
        'ambiguous-indent',
        line,
        badCol,
        line.start + diff,
        line.start + diff + 1,
        `indent prefix is visually at column ${col} but differs from the enclosing level`,
      );
      i = recover(i);
      continue;
    }

    const item = parseItem(line);
    if (!item) {
      report('bad-line', line, 1, line.start, line.textEnd, 'expected `key:` or `key: value`');
      i = recover(i);
      continue;
    }

    if (item.hasValue) {
      const leaf: LeafNode = {
        kind: 'leaf',
        start: line.start,
        end: line.end,
        key: item.key,
        keyStart: item.keyStart,
        keyEnd: item.keyEnd,
        value: item.value,
        valueStart: item.valueStart,
        valueEnd: item.valueEnd,
      };
      container().children.push(leaf);
      pending = null;
    } else {
      const block: BlockNode = {
        kind: 'block',
        start: line.start,
        headerEnd: line.end,
        end: line.end,
        key: item.key,
        keyStart: item.keyStart,
        keyEnd: item.keyEnd,
        children: [],
      };
      container().children.push(block);
      pending = block;
    }
    i++;
  }

  while (stack.length > 0) closeLevel();
  return { root, errors };
}
