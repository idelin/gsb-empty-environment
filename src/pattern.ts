import { UnsupportedSchemaError, InvalidSchemaError } from './schema.ts';
import type { Rng } from './rng.ts';

/** 生成用的基础字母表（取反字符类也在此集合内取补）。 */
export const ALPHABET =
  'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789';
const DIGITS = '0123456789';
const WORD = ALPHABET + '_';
const SPACE = ' ';

export interface Segment {
  /** 可选字符集。 */
  chars: string;
  min: number;
  /** Infinity 表示无界。 */
  max: number;
}

export interface PatternAst {
  source: string;
  segments: Segment[];
  minLength: number;
  maxLength: number;
}

function escapeSet(chars: string): string {
  switch (chars) {
    case 'd':
      return DIGITS;
    case 'w':
      return WORD;
    case 's':
      return SPACE;
    default:
      return chars;
  }
}

function parseClassBody(pattern: string, start: number): { chars: string; end: number } {
  // start 指向 '[' 之后（可能是否定符 '^' 之后）
  let i = start;
  let chars = '';
  let negate = false;
  if (pattern[i] === '^') {
    negate = true;
    i++;
  }
  let first = true;
  while (i < pattern.length) {
    const c = pattern[i];
    if (c === ']' && !first) {
      const set = negate
        ? [...ALPHABET].filter((ch) => !chars.includes(ch)).join('')
        : chars;
      if (set.length === 0) {
        throw new InvalidSchemaError(`pattern: 字符类取反后为空（在生成字母表内）`);
      }
      return { chars: set, end: i + 1 };
    }
    first = false;
    if (c === '\\' && i + 1 < pattern.length) {
      chars += escapeSet(pattern[i + 1]);
      i += 2;
      continue;
    }
    if (i + 2 < pattern.length && pattern[i + 1] === '-' && pattern[i + 2] !== ']') {
      const from = c.charCodeAt(0);
      const to = pattern[i + 2].charCodeAt(0);
      if (from > to) {
        throw new InvalidSchemaError(`pattern: 非法字符范围 "${c}-${pattern[i + 2]}"`);
      }
      for (let code = from; code <= to; code++) chars += String.fromCharCode(code);
      i += 3;
      continue;
    }
    chars += c;
    i++;
  }
  throw new InvalidSchemaError('pattern: 字符类缺少 "]"');
}

/**
 * 解析受限 pattern 子集：必须 ^...$ 锚定；原子为字面字符、'.'、字符类、
 * \d \w \s 及转义字面量；量词为 * + ? {m} {m,} {m,n}。不支持分组与 '|'。
 */
export function parsePattern(pattern: string): PatternAst {
  if (
    typeof pattern !== 'string' ||
    pattern.length < 2 ||
    pattern[0] !== '^' ||
    pattern[pattern.length - 1] !== '$'
  ) {
    throw new UnsupportedSchemaError(
      `pattern "${pattern}": 仅支持 ^...$ 全串锚定的正则子集`,
    );
  }
  const body = pattern.slice(1, -1);
  const segments: Segment[] = [];
  let i = 0;

  const readAtom = (): string => {
    const c = body[i];
    if (c === '[') {
      const { chars, end } = parseClassBody(body, i + 1);
      i = end;
      return chars;
    }
    if (c === '\\') {
      if (i + 1 >= body.length) throw new InvalidSchemaError('pattern: 结尾孤立 "\\"');
      const chars = escapeSet(body[i + 1]);
      i += 2;
      return chars;
    }
    if (c === '.') {
      i++;
      return ALPHABET;
    }
    if ('()|'.includes(c)) {
      throw new UnsupportedSchemaError(
        `pattern: 不支持分组/alternation（"${c}"），只承诺字面量、字符类与量词`,
      );
    }
    i++;
    return c;
  };

  const readQuantifier = (): { min: number; max: number } => {
    const c = body[i];
    if (c === '*') {
      i++;
      return { min: 0, max: Infinity };
    }
    if (c === '+') {
      i++;
      return { min: 1, max: Infinity };
    }
    if (c === '?') {
      i++;
      return { min: 0, max: 1 };
    }
    if (c === '{') {
      const close = body.indexOf('}', i);
      if (close === -1) throw new InvalidSchemaError('pattern: 量词缺少 "}"');
      const inner = body.slice(i + 1, close);
      const match = /^(\d+)(?:,(\d*))?$/.exec(inner);
      if (!match) throw new InvalidSchemaError(`pattern: 非法量词 "{${inner}}"`);
      const min = Number(match[1]);
      const max = match[2] === undefined ? min : match[2] === '' ? Infinity : Number(match[2]);
      if (max < min) throw new InvalidSchemaError(`pattern: 量词 {${inner}} 上下界矛盾`);
      i = close + 1;
      return { min, max };
    }
    return { min: 1, max: 1 };
  };

  while (i < body.length) {
    const chars = readAtom();
    const { min, max } = readQuantifier();
    segments.push({ chars, min, max });
  }

  const minLength = segments.reduce((acc, s) => acc + s.min, 0);
  const maxLength = segments.reduce(
    (acc, s) => (acc === Infinity || s.max === Infinity ? Infinity : acc + s.max),
    0,
  );
  return { source: pattern, segments, minLength, maxLength };
}

/**
 * 生成匹配 pattern 且总长落在 [minLen, maxLen] 的字符串；
 * 区间与 pattern 可达长度无交集时返回 null（矛盾，由调用方报告）。
 */
export function generateFromPattern(
  ast: PatternAst,
  rng: Rng,
  minLen: number,
  maxLen: number,
): string | null {
  const lo = Math.max(minLen, ast.minLength);
  const hi = Math.min(maxLen, ast.maxLength);
  if (lo > hi) return null;
  const target = rng.int(lo, hi);
  const counts = ast.segments.map((s) => s.min);
  let remaining = target - counts.reduce((a, b) => a + b, 0);
  while (remaining > 0) {
    const growable = ast.segments
      .map((s, idx) => (counts[idx] < s.max ? idx : -1))
      .filter((idx) => idx >= 0);
    if (growable.length === 0) return null; // 理论上不会发生：target <= hi <= ast.maxLength
    counts[rng.pick(growable)]++;
    remaining--;
  }
  let out = '';
  ast.segments.forEach((seg, idx) => {
    for (let k = 0; k < counts[idx]; k++) out += seg.chars[rng.int(0, seg.chars.length - 1)];
  });
  return out;
}
