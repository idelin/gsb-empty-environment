/**
 * Statement extraction and stable statement keys.
 *
 * A statement is either:
 *  - a simple statement: a run of tokens terminated by `;` at block level
 *    (or by end of file), or
 *  - a compound statement: a header (`if (x)`, `function f()`, `=>`, ...)
 *    plus its `{ ... }` block. Inner statements are extracted separately,
 *    so statements may nest; breakpoints resolve to the innermost one.
 *
 * Every statement gets a `key` that defines its *identity* across versions:
 *  - simple statement: normalized token text (whitespace/comments removed),
 *  - compound statement: normalized header text + a block marker, so edits
 *    inside the body do not change the identity of the enclosing statement.
 *
 * Because keys are content-based and position-independent, a statement that
 * is *moved* keeps its key, while a *deleted* statement's key disappears.
 */

import { tokenize } from './tokenizer.ts';
import type { Token } from './tokenizer.ts';

export interface Statement {
  /** Identity key, stable across versions as long as content is unchanged. */
  key: string;
  /** Start offset (inclusive) and end offset (exclusive) in the source. */
  start: number;
  end: number;
  /** 1-based line and character of the statement start. */
  startLine: number;
  startChar: number;
  /** 1-based line and character of the statement end. */
  endLine: number;
  endChar: number;
  /** True for header+block statements (if/for/function/...). */
  compound: boolean;
}

const COMPOUND_PREFIX = '##block:';

const BLOCK_PREV_PUNCT = new Set([')', '{', '}', ';', '=>']);
const BLOCK_PREV_WORD = new Set(['else', 'try', 'finally', 'do']);

export function computeLineStarts(source: string): number[] {
  const starts = [0];
  for (let i = 0; i < source.length; i++) {
    if (source[i] === '\n') starts.push(i + 1);
  }
  return starts;
}

export function offsetToLineChar(lineStarts: number[], offset: number): { line: number; character: number } {
  let lo = 0;
  let hi = lineStarts.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (lineStarts[mid] <= offset) lo = mid; else hi = mid - 1;
  }
  return { line: lo + 1, character: offset - lineStarts[lo] + 1 };
}

export function lineCharToOffset(lineStarts: number[], sourceLength: number, line: number, character: number): number {
  const lineIndex = Math.min(Math.max(line - 1, 0), lineStarts.length - 1);
  return Math.min(lineStarts[lineIndex] + Math.max(character - 1, 0), sourceLength);
}

function joinTokens(tokens: Token[], from: number, toInclusive: number): string {
  const parts: string[] = [];
  for (let i = from; i <= toInclusive; i++) parts.push(tokens[i].value);
  return parts.join(' ');
}

interface BraceFrame {
  block: boolean;
  segStart: number;
  openIdx: number;
}

export function extractStatements(source: string): Statement[] {
  const tokens = tokenize(source);
  const lineStarts = computeLineStarts(source);
  const statements: Statement[] = [];

  const stack: Array<'(' | '[' | BraceFrame> = [];
  let segStart = 0; // token index where the current segment begins

  const topAllowsSemicolon = (): boolean => {
    const top = stack[stack.length - 1];
    return top === undefined || (typeof top === 'object' && top.block);
  };

  const push = (key: string, start: number, end: number, compound: boolean): void => {
    const s = offsetToLineChar(lineStarts, start);
    const e = offsetToLineChar(lineStarts, end);
    statements.push({
      key, start, end, compound,
      startLine: s.line, startChar: s.character,
      endLine: e.line, endChar: e.character,
    });
  };

  for (let idx = 0; idx < tokens.length; idx++) {
    const t = tokens[idx];
    if (t.type !== 'punct') continue;

    if (t.value === '(' || t.value === '[') {
      stack.push(t.value);
    } else if (t.value === ')' || t.value === ']') {
      // Pop the matching opener if it is on top; tolerate imbalance.
      const top = stack[stack.length - 1];
      if (top === '(' || top === '[') stack.pop();
    } else if (t.value === '{') {
      const p = tokens[idx - 1];
      const isBlock = !p ||
        (p.type === 'punct' && BLOCK_PREV_PUNCT.has(p.value)) ||
        (p.type === 'word' && BLOCK_PREV_WORD.has(p.value));
      stack.push({ block: isBlock, segStart, openIdx: idx });
      if (isBlock) {
        // The header belongs to the compound statement; inner statements
        // start a fresh segment after the opening brace.
        segStart = idx + 1;
      }
    } else if (t.value === '}') {
      const frame = stack.pop();
      if (typeof frame === 'object' && frame.block) {
        const header = frame.openIdx > frame.segStart
          ? joinTokens(tokens, frame.segStart, frame.openIdx - 1)
          : '';
        push(COMPOUND_PREFIX + header, tokens[frame.segStart].start, t.end, true);
        segStart = idx + 1;
      }
    } else if (t.value === ';' && topAllowsSemicolon()) {
      // segStart === idx means the segment is a lone `;` (empty statement): skip it.
      if (segStart < idx) {
        push(joinTokens(tokens, segStart, idx), tokens[segStart].start, t.end, false);
      }
      segStart = idx + 1;
    }
  }

  // Trailing tokens without a terminating semicolon (ASI).
  if (segStart < tokens.length) {
    const last = tokens[tokens.length - 1];
    push(joinTokens(tokens, segStart, tokens.length - 1), tokens[segStart].start, last.end, false);
  }

  // Compound statements are emitted at their closing brace; restore
  // document order (outer statements before the ones they contain).
  statements.sort((a, b) => a.start - b.start || b.end - a.end);

  return statements;
}
