import { Ast } from './ast';

/** 捕获区间：[start, end)。 */
export type Span = readonly [number, number];
/** 捕获表：组号（从 1 开始） -> 区间。未参与的组不在表中。 */
export type Caps = ReadonlyMap<number, Span>;
/** 一次可能的匹配：结束位置 + 捕获表（起点由调用方给定）。 */
export interface RawMatch {
  end: number;
  caps: Caps;
}

const NO_CAPS: Caps = new Map();

function capsKey(caps: Caps): string {
  const parts: string[] = [];
  for (const [g, [s, e]] of [...caps.entries()].sort((a, b) => a[0] - b[0])) {
    parts.push(`${g}:${s}-${e}`);
  }
  return parts.join(',');
}

function matchKey(m: RawMatch): string {
  return `${m.end}|${capsKey(m.caps)}`;
}

/** 后者覆盖前者（星号的后续迭代覆盖先前任期同一组的捕获）。 */
function mergeCaps(base: Caps, over: Caps): Caps {
  if (over.size === 0) return base;
  const merged = new Map(base);
  for (const [g, span] of over) merged.set(g, span);
  return merged;
}

function dedup(matches: RawMatch[]): RawMatch[] {
  const seen = new Set<string>();
  const out: RawMatch[] = [];
  for (const m of matches) {
    const key = matchKey(m);
    if (!seen.has(key)) {
      seen.add(key);
      out.push(m);
    }
  }
  return out;
}

/**
 * 穷举 node 在 s 的 pos 处能产生的所有匹配（结果按 (end, caps) 去重）。
 * 每个 (节点 id, pos) 记忆化一次；星号用工作表法求不动点，
 * 状态 (end, caps) 集合有限且单调增长，因此空匹配循环必然终止。
 */
export function enumerate(
  node: Ast,
  s: string,
  pos: number,
  memo: Map<string, RawMatch[]>,
): RawMatch[] {
  const key = `${node.id}:${pos}`;
  const hit = memo.get(key);
  if (hit) return hit;
  const result = compute(node, s, pos, memo);
  memo.set(key, result);
  return result;
}

function compute(node: Ast, s: string, pos: number, memo: Map<string, RawMatch[]>): RawMatch[] {
  switch (node.kind) {
    case 'char':
      return s[pos] === node.ch ? [{ end: pos + 1, caps: NO_CAPS }] : [];

    case 'concat': {
      let current: RawMatch[] = [{ end: pos, caps: NO_CAPS }];
      for (const part of node.parts) {
        const next: RawMatch[] = [];
        for (const m of current) {
          for (const r of enumerate(part, s, m.end, memo)) {
            next.push({ end: r.end, caps: mergeCaps(m.caps, r.caps) });
          }
        }
        current = dedup(next);
        if (current.length === 0) break;
      }
      return current;
    }

    case 'alt': {
      const out: RawMatch[] = [];
      for (const option of node.options) {
        out.push(...enumerate(option, s, pos, memo));
      }
      return dedup(out);
    }

    case 'group': {
      const inner = enumerate(node.child, s, pos, memo);
      return inner.map((m) => ({
        end: m.end,
        caps: mergeCaps(m.caps, new Map([[node.index, [pos, m.end] as Span]])),
      }));
    }

    case 'star': {
      // 不动点闭包：从“零次迭代”出发，不断用子节点的一次匹配扩展，
      // 已见状态（end + 捕获表）不再重复扩展 —— 空迭代不会让状态数无限增长。
      const seed: RawMatch = { end: pos, caps: NO_CAPS };
      const results: RawMatch[] = [seed];
      const seen = new Set<string>([matchKey(seed)]);
      const queue: RawMatch[] = [seed];
      while (queue.length > 0) {
        const cur = queue.pop()!;
        for (const step of enumerate(node.child, s, cur.end, memo)) {
          const next: RawMatch = { end: step.end, caps: mergeCaps(cur.caps, step.caps) };
          const k = matchKey(next);
          if (!seen.has(k)) {
            seen.add(k);
            results.push(next);
            queue.push(next);
          }
        }
      }
      return results;
    }
  }
}

/**
 * 匹配优先级（同一起点下）：先比整体长度，再按组号顺序比各组长度。
 * 未参与的组记为 -1，因此“未参与”严格小于“参与但为空串”。
 * 全部长度并列时按组序比较区间，起点靠后者胜，其次终点靠后者胜
 * （与“星号后迭代覆盖先迭代”的约定一致），保证结果唯一确定。
 */
export function compare(a: RawMatch, b: RawMatch, groupCount: number): number {
  if (a.end !== b.end) return b.end - a.end; // 整体更长者胜
  for (let g = 1; g <= groupCount; g++) {
    const sa = a.caps.get(g);
    const sb = b.caps.get(g);
    const la = sa === undefined ? -1 : sa[1] - sa[0];
    const lb = sb === undefined ? -1 : sb[1] - sb[0];
    if (la !== lb) return lb - la; // 该组更长者胜
  }
  for (let g = 1; g <= groupCount; g++) {
    const sa = a.caps.get(g);
    const sb = b.caps.get(g);
    if (sa === undefined || sb === undefined) continue; // 长度并列时已同为 -1 或同长
    if (sa[0] !== sb[0]) return sb[0] - sa[0]; // 起点靠后者胜
    if (sa[1] !== sb[1]) return sb[1] - sa[1]; // 终点靠后者胜
  }
  return 0;
}

export interface FullMatch extends RawMatch {
  start: number;
}

/** 最左起点：从 0 起扫描，第一个存在任何匹配（含空匹配）的起点获胜。 */
export function exec(ast: Ast, groupCount: number, input: string): FullMatch | null {
  const memo = new Map<string, RawMatch[]>();
  for (let start = 0; start <= input.length; start++) {
    const results = enumerate(ast, input, start, memo);
    if (results.length === 0) continue;
    let best = results[0]!;
    for (let i = 1; i < results.length; i++) {
      if (compare(results[i]!, best, groupCount) < 0) best = results[i]!;
    }
    return { start, end: best.end, caps: best.caps };
  }
  return null;
}
