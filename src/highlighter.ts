/**
 * 增量词法着色器
 *
 * 小语言特性：
 *   - 标识符 / 关键字（let fn return if else while）
 *   - 数字字面量
 *   - 字符串 "..."，支持 \" 与 \\ 转义（转义引号不结束字符串），不跨行
 *   - 行注释 // ... 与跨行块注释 /* ... *​/
 *
 * 增量策略：
 *   - 每行缓存：入口状态（Normal / InBlockComment）、行内 token（相对行首偏移）
 *   - 编辑后从首个受影响行重扫，逐行比较出口状态与旧缓存的入口状态，
 *     一旦一致（状态稳定）即停止，后续行直接复用缓存
 *
 * 偏移约定：所有对外偏移均为 UTF-16 code unit（即 JS 字符串下标），
 * 词法扫描按码点前进，保证 token 边界绝不切开代理对。
 */

export const STATE_NORMAL = 0;
export const STATE_IN_BLOCK_COMMENT = 1;
export type LexerState = typeof STATE_NORMAL | typeof STATE_IN_BLOCK_COMMENT;

export type TokenType =
  | 'keyword'
  | 'identifier'
  | 'number'
  | 'string'
  | 'comment'
  | 'operator';

export interface Token {
  type: TokenType;
  /** UTF-16 偏移（相对所属行行首，getTokens() 输出时为文档绝对偏移） */
  start: number;
  end: number;
}

export interface InvalidatedRange {
  /** 首个失效行（0 基，含） */
  from: number;
  /** 最后一个失效行（0 基，含）；from > to 表示无失效行 */
  to: number;
}

export interface EditResult {
  invalidatedLines: InvalidatedRange;
  lineCount: number;
}

const KEYWORDS = new Set(['let', 'fn', 'return', 'if', 'else', 'while']);

function isDigit(code: number): boolean {
  return code >= 0x30 && code <= 0x39;
}

function isAsciiIdentStart(code: number): boolean {
  return (
    (code >= 0x61 && code <= 0x7a) || // a-z
    (code >= 0x41 && code <= 0x5a) || // A-Z
    code === 0x5f || // _
    code === 0x24 // $
  );
}

/** 按码点判断标识符字符；非 ASCII 码点（含代理对表示的星平面字符）整体视为标识符字符 */
function isIdentCharAt(text: string, index: number): { ok: boolean; width: number } {
  const code = text.codePointAt(index)!;
  if (code > 0xffff) return { ok: true, width: 2 }; // 代理对，整体前进，绝不切开
  if (isAsciiIdentStart(code) || isDigit(code)) return { ok: true, width: 1 };
  if (code >= 0x80) return { ok: true, width: 1 }; // BMP 非 ASCII（如中文）
  return { ok: false, width: 1 };
}

/** 扫描单行，产出相对行首偏移的 token，返回出口状态 */
export function lexLine(line: string, entryState: LexerState, out: Token[]): LexerState {
  const len = line.length;
  let i = 0;

  if (entryState === STATE_IN_BLOCK_COMMENT) {
    const close = line.indexOf('*/');
    if (close === -1) {
      if (len > 0) out.push({ type: 'comment', start: 0, end: len });
      return STATE_IN_BLOCK_COMMENT;
    }
    out.push({ type: 'comment', start: 0, end: close + 2 });
    i = close + 2;
  }

  while (i < len) {
    const ch = line.charCodeAt(i);

    // 空白
    if (ch === 0x20 || ch === 0x09 || ch === 0x0b || ch === 0x0c) {
      i++;
      continue;
    }

    // 注释
    if (ch === 0x2f && i + 1 < len) {
      const next = line.charCodeAt(i + 1);
      if (next === 0x2f) {
        // // 行注释
        out.push({ type: 'comment', start: i, end: len });
        return STATE_NORMAL;
      }
      if (next === 0x2a) {
        // /* 块注释
        const close = line.indexOf('*/', i + 2);
        if (close === -1) {
          out.push({ type: 'comment', start: i, end: len });
          return STATE_IN_BLOCK_COMMENT;
        }
        out.push({ type: 'comment', start: i, end: close + 2 });
        i = close + 2;
        continue;
      }
    }

    // 字符串：\" 与 \\ 转义，不跨行
    if (ch === 0x22) {
      let j = i + 1;
      let terminated = false;
      while (j < len) {
        const c = line.charCodeAt(j);
        if (c === 0x5c) {
          // 反斜杠：跳过被转义的下一个码点（代理对整体跳过）
          const cp = line.codePointAt(j + 1);
          j += 1 + (cp !== undefined && cp > 0xffff ? 2 : 1);
          continue;
        }
        if (c === 0x22) {
          terminated = true;
          j++;
          break;
        }
        j++;
      }
      out.push({ type: 'string', start: i, end: terminated ? j : len });
      i = terminated ? j : len;
      continue;
    }

    // 数字
    if (isDigit(ch)) {
      let j = i + 1;
      while (j < len) {
        const c = line.charCodeAt(j);
        if (isDigit(c) || c === 0x2e || (c >= 0x61 && c <= 0x66) || (c >= 0x41 && c <= 0x46) || c === 0x78) {
          j++;
        } else {
          break;
        }
      }
      out.push({ type: 'number', start: i, end: j });
      i = j;
      continue;
    }

    // 标识符 / 关键字（按码点前进，不切开代理对）
    const ident = isIdentCharAt(line, i);
    if (ident.ok && !isDigit(line.charCodeAt(i))) {
      let j = i + ident.width;
      while (j < len) {
        const cont = isIdentCharAt(line, j);
        if (!cont.ok) break;
        j += cont.width;
      }
      const word = line.slice(i, j);
      out.push({ type: KEYWORDS.has(word) ? 'keyword' : 'identifier', start: i, end: j });
      i = j;
      continue;
    }

    // 其余：运算符 / 标点（单码点）
    const cp = line.codePointAt(i)!;
    out.push({ type: 'operator', start: i, end: i + (cp > 0xffff ? 2 : 1) });
    i += cp > 0xffff ? 2 : 1;
  }

  return STATE_NORMAL;
}

interface SplitLines {
  lines: string[];
  /** 每行起始的 UTF-16 绝对偏移 */
  starts: number[];
}

/** 同时支持 LF 与 CRLF（以及孤立 CR），行文本不含行终止符 */
export function splitLines(text: string): SplitLines {
  const lines: string[] = [];
  const starts: number[] = [];
  let start = 0;
  let i = 0;
  const len = text.length;
  while (i < len) {
    const c = text.charCodeAt(i);
    if (c === 0x0a) {
      lines.push(text.slice(start, i));
      starts.push(start);
      i++;
      start = i;
    } else if (c === 0x0d) {
      lines.push(text.slice(start, i));
      starts.push(start);
      i++;
      if (i < len && text.charCodeAt(i) === 0x0a) i++; // CRLF 整体消费
      start = i;
    } else {
      i++;
    }
  }
  lines.push(text.slice(start));
  starts.push(start);
  return { lines, starts };
}

/** 若 index 落在代理对中间，向前对齐到码点边界 */
function snapToCodePointBoundary(text: string, index: number): number {
  if (index > 0 && index < text.length) {
    const lead = text.charCodeAt(index - 1);
    const trail = text.charCodeAt(index);
    if (lead >= 0xd800 && lead <= 0xdbff && trail >= 0xdc00 && trail <= 0xdfff) {
      return index - 1;
    }
  }
  return index;
}

export class IncrementalHighlighter {
  private text = '';
  private lines: string[] = [''];
  private lineStarts: number[] = [0];
  /** entryStates[i] = 第 i 行入口状态；entryStates[lines.length] = 文末状态 */
  private entryStates: LexerState[] = [STATE_NORMAL, STATE_NORMAL];
  /** 每行 token，偏移相对行首 */
  private lineTokens: Token[][] = [[]];
  private invalidated: InvalidatedRange = { from: 0, to: 0 };

  constructor(text = '') {
    this.setText(text);
  }

  get lineCount(): number {
    return this.lines.length;
  }

  getText(): string {
    return this.text;
  }

  /** 全量重建 */
  setText(text: string): void {
    this.text = text;
    const { lines, starts } = splitLines(text);
    this.lines = lines;
    this.lineStarts = starts;
    this.entryStates = new Array(lines.length + 1).fill(STATE_NORMAL);
    this.lineTokens = new Array(lines.length);
    let state: LexerState = STATE_NORMAL;
    for (let i = 0; i < lines.length; i++) {
      this.entryStates[i] = state;
      const tokens: Token[] = [];
      state = lexLine(lines[i], state, tokens);
      this.lineTokens[i] = tokens;
    }
    this.entryStates[lines.length] = state;
    this.invalidated = { from: 0, to: lines.length - 1 };
  }

  /**
   * 应用编辑。偏移为 UTF-16 code unit；若边界切开代理对会自动对齐到码点边界。
   * 仅重扫首个受影响行到状态稳定行为止，返回失效行范围。
   */
  applyEdit(start: number, deleteCount: number, insertText: string): EditResult {
    const oldText = this.text;
    start = Math.max(0, Math.min(start, oldText.length));
    start = snapToCodePointBoundary(oldText, start);
    let end = Math.max(start, Math.min(start + deleteCount, oldText.length));
    end = snapToCodePointBoundary(oldText, end);

    const newText = oldText.slice(0, start) + insertText + oldText.slice(end);

    const oldLines = this.lines;
    const oldStates = this.entryStates;
    const oldTokens = this.lineTokens;

    const { lines: newLines, starts: newStarts } = splitLines(newText);

    // 公共前缀行（文本相同 => 偏移也相同）
    let prefix = 0;
    while (
      prefix < oldLines.length &&
      prefix < newLines.length &&
      oldLines[prefix] === newLines[prefix]
    ) {
      prefix++;
    }
    // 公共后缀行
    let suffix = 0;
    while (
      suffix < oldLines.length - prefix &&
      suffix < newLines.length - prefix &&
      oldLines[oldLines.length - 1 - suffix] === newLines[newLines.length - 1 - suffix]
    ) {
      suffix++;
    }

    const lineDelta = newLines.length - oldLines.length;
    const newStates: LexerState[] = new Array(newLines.length + 1);
    const newTokens: Token[][] = new Array(newLines.length);

    // 前缀行直接复用
    for (let i = 0; i < prefix; i++) {
      newStates[i] = oldStates[i];
      newTokens[i] = oldTokens[i];
    }

    const invalidatedFrom = prefix;
    let invalidatedTo = prefix - 1;

    // 从首个受影响行重扫；进入未变后缀且状态与旧缓存一致即停止
    let i = prefix;
    let state: LexerState = oldStates[Math.min(prefix, oldLines.length)];
    let stabilized = false;
    while (i < newLines.length) {
      if (i >= newLines.length - suffix) {
        const oldIndex = i - lineDelta;
        if (oldIndex >= prefix && state === oldStates[oldIndex]) {
          stabilized = true;
          break;
        }
      }
      const tokens: Token[] = [];
      newStates[i] = state;
      state = lexLine(newLines[i], state, tokens);
      newTokens[i] = tokens;
      invalidatedTo = i;
      i++;
    }

    // 状态稳定后的后缀行：复用旧缓存（行号与行首偏移平移即可）
    for (; i < newLines.length; i++) {
      const oldIndex = i - lineDelta;
      newStates[i] = oldStates[oldIndex];
      newTokens[i] = oldTokens[oldIndex];
    }
    newStates[newLines.length] = stabilized
      ? oldStates[oldLines.length]
      : state;

    this.text = newText;
    this.lines = newLines;
    this.lineStarts = newStarts;
    this.entryStates = newStates;
    this.lineTokens = newTokens;
    this.invalidated = { from: invalidatedFrom, to: invalidatedTo };

    return {
      invalidatedLines: this.invalidated,
      lineCount: newLines.length,
    };
  }

  /** 上一次编辑的失效行范围 */
  getInvalidatedLines(): InvalidatedRange {
    return this.invalidated;
  }

  /** 全文档 token，绝对 UTF-16 偏移 */
  getTokens(): Token[] {
    const out: Token[] = [];
    for (let i = 0; i < this.lines.length; i++) {
      const base = this.lineStarts[i];
      for (const t of this.lineTokens[i]) {
        out.push({ type: t.type, start: base + t.start, end: base + t.end });
      }
    }
    return out;
  }

  /** 单行 token，绝对 UTF-16 偏移 */
  getTokensForLine(line: number): Token[] {
    const base = this.lineStarts[line];
    return this.lineTokens[line].map((t) => ({
      type: t.type,
      start: base + t.start,
      end: base + t.end,
    }));
  }

  /** 第 i 行入口状态（调试用） */
  getEntryState(line: number): LexerState {
    return this.entryStates[line];
  }
}
