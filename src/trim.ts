import { TrimError } from './errors.ts';
import { parseExpr, evalExpr } from './expr.ts';
import { Lexer } from './lexer.ts';

export interface TrimResult {
  /** The trimmed source text (kept lines verbatim, original line endings). */
  readonly text: string;
  /**
   * 1-based forward map: oldToNew[oldLine] is the line number in the trimmed
   * text, or null when the original line was removed (directive or dead
   * branch). Index 0 is unused and always null.
   */
  readonly oldToNew: readonly (number | null)[];
  /** 1-based reverse map: newToOld[newLine] is the original line number. */
  readonly newToOld: readonly number[];
  /** Forward query; returns null (explicitly empty) for removed lines. */
  toNewLine(oldLine: number): number | null;
  /** Reverse query; throws RangeError for out-of-range new lines. */
  toOldLine(newLine: number): number;
}

interface Frame {
  /** Line where the enclosing '#if' opened. */
  readonly line: number;
  /** Whether the enclosing context emits lines. */
  readonly parentActive: boolean;
  /** Whether the current branch emits lines. */
  active: boolean;
  /** Whether some branch of this frame has already been taken. */
  branchTaken: boolean;
  elseSeen: boolean;
}

type DirectiveKeyword = 'if' | 'elif' | 'else' | 'endif';

interface Directive {
  readonly keyword: DirectiveKeyword;
  /** Text after the keyword (condition for if/elif, must be empty/comment otherwise). */
  readonly rest: string;
}

const KEYWORDS: ReadonlySet<string> = new Set(['if', 'elif', 'else', 'endif']);

/**
 * Recognizes a directive line: optional whitespace, '#', keyword, then the
 * rest of the line. Returns null for anything else (unknown '#xxx' lines are
 * treated as ordinary code).
 */
function parseDirectiveLine(line: string): Directive | null {
  let i = 0;
  while (i < line.length && (line[i] === ' ' || line[i] === '\t')) i++;
  if (line[i] !== '#') return null;
  let j = i + 1;
  while (j < line.length && /[a-z]/.test(line[j])) j++;
  const word = line.slice(i + 1, j);
  if (!KEYWORDS.has(word)) return null;
  return { keyword: word as DirectiveKeyword, rest: line.slice(j) };
}

/** Conditions contain no string literals, so cutting at comment starts is safe. */
function stripTrailingComment(text: string): string {
  let cut = text.length;
  const lineComment = text.indexOf('//');
  const blockComment = text.indexOf('/*');
  if (lineComment !== -1) cut = Math.min(cut, lineComment);
  if (blockComment !== -1) cut = Math.min(cut, blockComment);
  return text.slice(0, cut);
}

function splitLines(source: string): { text: string; eol: string }[] {
  const lines: { text: string; eol: string }[] = [];
  let start = 0;
  for (let i = 0; i < source.length; i++) {
    const ch = source[i];
    if (ch === '\n') {
      lines.push({ text: source.slice(start, i), eol: '\n' });
      start = i + 1;
    } else if (ch === '\r') {
      if (source[i + 1] === '\n') {
        lines.push({ text: source.slice(start, i), eol: '\r\n' });
        i++;
      } else {
        lines.push({ text: source.slice(start, i), eol: '\r' });
      }
      start = i + 1;
    }
  }
  if (start < source.length) {
    lines.push({ text: source.slice(start), eol: '' });
  }
  return lines;
}

function handleDirective(
  directive: Directive,
  line: number,
  frames: Frame[],
  vars: Record<string, boolean>,
): void {
  const top = frames[frames.length - 1];
  switch (directive.keyword) {
    case 'if': {
      const parentActive = frames.length === 0 || frames[frames.length - 1].active;
      const exprText = stripTrailingComment(directive.rest).trim();
      if (exprText === '') {
        throw new TrimError('INVALID_EXPRESSION', line, "missing condition after '#if'");
      }
      // Always parse (syntax is checked even in dead branches); evaluate only
      // when the enclosing context is active, so dead branches never raise
      // unknown-variable errors.
      const expr = parseExpr(exprText, line);
      const cond = parentActive ? evalExpr(expr, vars, line) : false;
      frames.push({
        line,
        parentActive,
        active: parentActive && cond,
        branchTaken: parentActive && cond,
        elseSeen: false,
      });
      break;
    }
    case 'elif': {
      if (!top) {
        throw new TrimError('UNEXPECTED_DIRECTIVE', line, "'#elif' without matching '#if'");
      }
      if (top.elseSeen) {
        throw new TrimError('ELIF_AFTER_ELSE', line, "'#elif' after '#else'");
      }
      const exprText = stripTrailingComment(directive.rest).trim();
      if (exprText === '') {
        throw new TrimError('INVALID_EXPRESSION', line, "missing condition after '#elif'");
      }
      if (!top.parentActive || top.branchTaken) {
        top.active = false;
        break;
      }
      const cond = evalExpr(parseExpr(exprText, line), vars, line);
      top.active = cond;
      top.branchTaken = cond;
      break;
    }
    case 'else': {
      if (!top) {
        throw new TrimError('UNEXPECTED_DIRECTIVE', line, "'#else' without matching '#if'");
      }
      if (top.elseSeen) {
        throw new TrimError('DUPLICATE_ELSE', line, "duplicate '#else'");
      }
      const extra = stripTrailingComment(directive.rest).trim();
      if (extra !== '') {
        throw new TrimError('INVALID_EXPRESSION', line, "unexpected content after '#else'");
      }
      top.elseSeen = true;
      top.active = top.parentActive && !top.branchTaken;
      top.branchTaken = true;
      break;
    }
    case 'endif': {
      if (!top) {
        throw new TrimError('UNEXPECTED_DIRECTIVE', line, "'#endif' without matching '#if'");
      }
      const extra = stripTrailingComment(directive.rest).trim();
      if (extra !== '') {
        throw new TrimError('INVALID_EXPRESSION', line, "unexpected content after '#endif'");
      }
      frames.pop();
      break;
    }
  }
}

/**
 * Trims `source` by evaluating `#if` / `#elif` / `#else` / `#endif`
 * directives against `vars`. Directive lines and dead-branch lines are
 * removed; the returned mapping relates line numbers in both directions.
 */
export function trimSource(source: string, vars: Record<string, boolean> = {}): TrimResult {
  const lines = splitLines(source);
  const lexer = new Lexer();
  const frames: Frame[] = [];
  const outParts: string[] = [];
  const oldToNew: (number | null)[] = [null];
  const newToOld: number[] = [0];

  const isActive = (): boolean => frames.length === 0 || frames[frames.length - 1].active;

  for (let idx = 0; idx < lines.length; idx++) {
    const lineNo = idx + 1;
    const { text, eol } = lines[idx];

    if (lexer.inCode) {
      const directive = parseDirectiveLine(text);
      if (directive) {
        handleDirective(directive, lineNo, frames, vars);
        oldToNew.push(null);
        lexer.advanceLine(text);
        continue;
      }
    }

    if (isActive()) {
      outParts.push(text, eol);
      newToOld.push(lineNo);
      oldToNew.push(newToOld.length - 1);
    } else {
      oldToNew.push(null);
    }
    lexer.advanceLine(text);
  }

  if (frames.length > 0) {
    const open = frames[frames.length - 1];
    throw new TrimError('UNCLOSED_CONDITION', open.line, `unclosed '#if' opened here`);
  }

  const oldLineCount = lines.length;
  const newLineCount = newToOld.length - 1;

  return {
    text: outParts.join(''),
    oldToNew,
    newToOld,
    toNewLine(oldLine: number): number | null {
      if (!Number.isInteger(oldLine) || oldLine < 1 || oldLine > oldLineCount) {
        throw new RangeError(`old line ${oldLine} out of range 1..${oldLineCount}`);
      }
      return oldToNew[oldLine];
    },
    toOldLine(newLine: number): number {
      if (!Number.isInteger(newLine) || newLine < 1 || newLine > newLineCount) {
        throw new RangeError(`new line ${newLine} out of range 1..${newLineCount}`);
      }
      return newToOld[newLine];
    },
  };
}
