import { RuleError } from './errors.ts';
import { parsePath } from './path.ts';

export type PatternSegment =
  | { kind: 'lit'; value: string }
  | { kind: 'one' }   // `*`  : exactly one segment, captured
  | { kind: 'many' }; // `**` : zero or more segments, captured

export type Capture =
  | { kind: 'one'; value: string }
  | { kind: 'many'; values: string[] };

export interface CompiledRule {
  index: number;
  fromRaw: string;
  toRaw: string;
  from: PatternSegment[];
  to: PatternSegment[];
}

function compileSegments(raw: string, ruleIndex: number, side: 'from' | 'to'): PatternSegment[] {
  return parsePath(raw).map((seg) => {
    if (seg === '*') return { kind: 'one' } as const;
    if (seg === '**') return { kind: 'many' } as const;
    if (seg.includes('*')) {
      throw new RuleError(ruleIndex, raw, raw, `segment ${JSON.stringify(seg)} in ${side} pattern: \`*\` may only appear as a whole segment`);
    }
    return { kind: 'lit', value: seg } as const;
  });
}

export function compileRule(fromRaw: string, toRaw: string, index: number): CompiledRule {
  const from = compileSegments(fromRaw, index, 'from');
  const to = compileSegments(toRaw, index, 'to');
  const count = (segs: PatternSegment[], kind: 'one' | 'many'): number => segs.filter((s) => s.kind === kind).length;
  for (const kind of ['one', 'many'] as const) {
    const inFrom = count(from, kind);
    const inTo = count(to, kind);
    if (inFrom !== inTo) {
      const token = kind === 'one' ? '*' : '**';
      throw new RuleError(index, fromRaw, toRaw, `capture count mismatch for ${token}: source has ${inFrom}, target has ${inTo}`);
    }
  }
  return { index, fromRaw, toRaw, from, to };
}

/** Match a concrete path against a pattern; returns captures or null. */
export function matchPattern(pattern: PatternSegment[], segments: string[]): Capture[] | null {
  const walk = (pi: number, si: number, caps: Capture[]): Capture[] | null => {
    if (pi === pattern.length) return si === segments.length ? caps : null;
    const seg = pattern[pi] as PatternSegment;
    if (seg.kind === 'lit') {
      return si < segments.length && segments[si] === seg.value ? walk(pi + 1, si + 1, caps) : null;
    }
    if (seg.kind === 'one') {
      return si < segments.length ? walk(pi + 1, si + 1, [...caps, { kind: 'one', value: segments[si] as string }]) : null;
    }
    for (let take = segments.length - si; take >= 0; take -= 1) {
      const hit = walk(pi + 1, si + take, [...caps, { kind: 'many', values: segments.slice(si, si + take) }]);
      if (hit) return hit;
    }
    return null;
  };
  return walk(0, 0, []);
}

/** Substitute captures into a target pattern, producing a concrete path. */
export function buildTarget(pattern: PatternSegment[], captures: Capture[]): string[] {
  const out: string[] = [];
  let ci = 0;
  for (const seg of pattern) {
    if (seg.kind === 'lit') {
      out.push(seg.value);
      continue;
    }
    const cap = captures[ci] as Capture;
    ci += 1;
    if (seg.kind === 'one' && cap.kind === 'one') out.push(cap.value);
    else if (seg.kind === 'many' && cap.kind === 'many') out.push(...cap.values);
  }
  return out;
}
