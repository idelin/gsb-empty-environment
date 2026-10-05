import type { Diagnostic } from './types.ts';

export interface BracketEvent {
  readonly open: boolean;
  readonly ch: string;
  readonly column: number;
}

/**
 * 单行分析结果。kind/indent/text/events/firstTabColumn 为行本地信息，
 * 只在该行文本变化时重新计算；depthBefore/diagnostics 为上下文状态，
 * 由 LineStore 的状态扫描维护。
 */
export interface LineAnalysis {
  readonly raw: string;
  readonly kind: 'blank' | 'comment' | 'code';
  /** 仅统计空格；Tab 是错误且不计入缩进宽度。 */
  readonly indent: number;
  readonly text: string;
  readonly events: readonly BracketEvent[];
  /** 行首空白中第一个 Tab 的列，-1 表示无。 */
  readonly firstTabColumn: number;
  /** 行首处的括号嵌套深度（>0 表示本行是括号续行）。 */
  depthBefore: number;
  /** 本行相关的诊断（绝对行号），每次扫描后重写。 */
  diagnostics: Diagnostic[];
}

const OPEN_BRACKETS = new Set(['(', '[', '{']);
const CLOSE_TO_OPEN = new Map([
  [')', '('],
  [']', '['],
  ['}', '{'],
]);

/** 行本地分词：只依赖本行文本，是增量重词法分析的最小单位。 */
export function analyzeLine(raw: string): LineAnalysis {
  let wsEnd = 0;
  let indent = 0;
  let firstTabColumn = -1;
  while (wsEnd < raw.length && (raw[wsEnd] === ' ' || raw[wsEnd] === '\t')) {
    if (raw[wsEnd] === '\t') {
      if (firstTabColumn < 0) firstTabColumn = wsEnd;
    } else {
      indent++;
    }
    wsEnd++;
  }
  const rest = raw.slice(wsEnd);
  const trimmed = rest.trim();

  if (trimmed.length === 0) {
    return { raw, kind: 'blank', indent, text: '', events: [], firstTabColumn, depthBefore: 0, diagnostics: [] };
  }
  if (trimmed.startsWith('#')) {
    return { raw, kind: 'comment', indent, text: trimmed, events: [], firstTabColumn, depthBefore: 0, diagnostics: [] };
  }

  // 代码行：# 之后为行注释，括号只统计注释之前的部分。
  const hashIndex = rest.indexOf('#');
  const codePart = hashIndex >= 0 ? rest.slice(0, hashIndex) : rest;
  const events: BracketEvent[] = [];
  for (let c = 0; c < codePart.length; c++) {
    const ch = codePart[c];
    if (OPEN_BRACKETS.has(ch)) {
      events.push({ open: true, ch, column: wsEnd + c });
    } else if (CLOSE_TO_OPEN.has(ch)) {
      events.push({ open: false, ch, column: wsEnd + c });
    }
  }
  return { raw, kind: 'code', indent, text: rest.trim(), events, firstTabColumn, depthBefore: 0, diagnostics: [] };
}

/**
 * 行缓存 + 括号状态扫描。
 * 编辑时只重新分词受影响的行；括号状态扫描是对缓存事件的线性遍历，
 * 不触碰原始文本，负责维护每行 depthBefore 与括号相关诊断。
 */
export class LineStore {
  private lines: LineAnalysis[];

  constructor(text: string) {
    this.lines = text.split('\n').map(analyzeLine);
    this.refreshBracketState();
  }

  get length(): number {
    return this.lines.length;
  }

  at(index: number): LineAnalysis {
    return this.lines[index];
  }

  all(): readonly LineAnalysis[] {
    return this.lines;
  }

  /** 应用行级编辑，返回重新分词的行数。 */
  splice(start: number, deleteCount: number, newRawLines: readonly string[]): { retokenized: number } {
    if (start < 0 || start > this.lines.length) {
      throw new RangeError(`startLine ${start} out of range (0..${this.lines.length})`);
    }
    if (deleteCount < 0 || start + deleteCount > this.lines.length) {
      throw new RangeError(`oldLineCount ${deleteCount} out of range at line ${start}`);
    }
    const inserted = newRawLines.map(analyzeLine);
    this.lines.splice(start, deleteCount, ...inserted);
    this.refreshBracketState();
    return { retokenized: inserted.length };
  }

  /** 全量括号状态扫描：维护 depthBefore、unmatched-close、unclosed-bracket、tab-in-indent。 */
  private refreshBracketState(): void {
    const stack: { ch: string; line: number; column: number }[] = [];
    for (let i = 0; i < this.lines.length; i++) {
      const line = this.lines[i];
      const diagnostics: Diagnostic[] = [];
      if (line.firstTabColumn >= 0) {
        diagnostics.push({
          code: 'tab-in-indent',
          line: i,
          column: line.firstTabColumn,
          message: 'Tab is not allowed in indentation; use spaces',
        });
      }
      line.depthBefore = stack.length;
      for (const event of line.events) {
        if (event.open) {
          stack.push({ ch: event.ch, line: i, column: event.column });
          continue;
        }
        const expected = CLOSE_TO_OPEN.get(event.ch);
        const top = stack[stack.length - 1];
        if (!top) {
          diagnostics.push({
            code: 'unmatched-close',
            line: i,
            column: event.column,
            message: `Closing '${event.ch}' has no matching opener`,
          });
        } else if (top.ch !== expected) {
          stack.pop();
          diagnostics.push({
            code: 'unmatched-close',
            line: i,
            column: event.column,
            message: `Closing '${event.ch}' does not match open '${top.ch}' from line ${top.line + 1}`,
          });
        } else {
          stack.pop();
        }
      }
      line.diagnostics = diagnostics;
    }
    for (const open of stack) {
      this.lines[open.line].diagnostics.push({
        code: 'unclosed-bracket',
        line: open.line,
        column: open.column,
        message: `Bracket '${open.ch}' is never closed`,
      });
    }
  }
}
