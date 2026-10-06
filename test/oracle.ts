/**
 * 验证预言机：显式枚举解析树，再对每棵树求值得到 (end, caps)，
 * 最后用独立实现的选取规则得出答案。与 src/matcher.ts 的不同之处在于：
 * 这里真的构造出每一棵解析树（星号展开成具体的迭代序列），
 * 通过对“状态 (位置, 捕获表)”去重来剪枝 —— 同一状态到达两次时，
 * 之后的扩展可能性完全相同，保留先到的树即可，因此空匹配循环必然终止。
 */
import { Ast } from '../src/ast';

export type Tree =
  | { t: 'char'; ch: string; pos: number }
  | { t: 'concat'; parts: Tree[]; pos: number; end: number }
  | { t: 'alt'; choice: number; child: Tree }
  | { t: 'star'; iterations: Tree[]; pos: number; end: number }
  | { t: 'group'; index: number; child: Tree; pos: number; end: number };

export interface EvalResult {
  end: number;
  caps: Map<number, [number, number]>;
}

/** 对一棵解析树求值：整体终点 + 各组捕获区间（后写的迭代覆盖先写的）。 */
export function evalTree(tree: Tree): EvalResult {
  switch (tree.t) {
    case 'char':
      return { end: tree.pos + 1, caps: new Map() };
    case 'concat': {
      const caps = new Map<number, [number, number]>();
      let end = tree.pos;
      for (const part of tree.parts) {
        const r = evalTree(part);
        for (const [g, span] of r.caps) caps.set(g, span);
        end = r.end;
      }
      return { end, caps };
    }
    case 'alt':
      return evalTree(tree.child);
    case 'star': {
      const caps = new Map<number, [number, number]>();
      let end = tree.pos;
      for (const iter of tree.iterations) {
        const r = evalTree(iter);
        for (const [g, span] of r.caps) caps.set(g, span);
        end = r.end;
      }
      return { end, caps };
    }
    case 'group': {
      const r = evalTree(tree.child);
      const caps = new Map(r.caps);
      caps.set(tree.index, [tree.pos, tree.end]);
      return { end: tree.end, caps };
    }
  }
}

function capsKey(caps: Map<number, [number, number]>): string {
  return [...caps.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([g, [s, e]]) => `${g}:${s}-${e}`)
    .join(',');
}

/** 枚举 node 在 pos 处的所有解析树（按状态剪枝后的代表树）。 */
export function genTrees(node: Ast, s: string, pos: number): Tree[] {
  switch (node.kind) {
    case 'char':
      return s[pos] === node.ch ? [{ t: 'char', ch: node.ch, pos }] : [];
    case 'concat': {
      let acc: Tree[][] = [[]];
      let ends: number[] = [pos];
      const empty: Tree = { t: 'concat', parts: [], pos, end: pos };
      for (const part of node.parts) {
        const nextAcc: Tree[][] = [];
        const nextEnds: number[] = [];
        for (let i = 0; i < acc.length; i++) {
          for (const t of genTrees(part, s, ends[i]!)) {
            nextAcc.push([...acc[i]!, t]);
            nextEnds.push(treeEnd(t, ends[i]!));
          }
        }
        acc = nextAcc;
        ends = nextEnds;
        if (acc.length === 0) return [];
      }
      if (node.parts.length === 0) return [empty];
      return acc.map((parts, i) => ({
        t: 'concat',
        parts,
        pos,
        end: ends[i]!,
      }));
    }
    case 'alt': {
      const out: Tree[] = [];
      node.options.forEach((option, choice) => {
        for (const child of genTrees(option, s, pos)) {
          out.push({ t: 'alt', choice, child });
        }
      });
      return out;
    }
    case 'group':
      return genTrees(node.child, s, pos).map((child) => ({
        t: 'group',
        index: node.index,
        child,
        pos,
        end: treeEnd(child, pos),
      }));
    case 'star': {
      // 广度优先枚举迭代序列；状态 = (当前位置, 累计捕获表)。
      const results: Tree[] = [{ t: 'star', iterations: [], pos, end: pos }];
      const seen = new Set<string>([`${pos}|`]);
      const queue: { iterations: Tree[]; pos: number; caps: Map<number, [number, number]> }[] = [
        { iterations: [], pos, caps: new Map() },
      ];
      while (queue.length > 0) {
        const cur = queue.shift()!;
        for (const iter of genTrees(node.child, s, cur.pos)) {
          const r = evalTree(iter);
          const caps = new Map(cur.caps);
          for (const [g, span] of r.caps) caps.set(g, span);
          const state = `${r.end}|${capsKey(caps)}`;
          if (seen.has(state)) continue;
          seen.add(state);
          const iterations = [...cur.iterations, iter];
          results.push({ t: 'star', iterations, pos, end: r.end });
          queue.push({ iterations, pos: r.end, caps });
        }
      }
      return results;
    }
  }
}

function treeEnd(tree: Tree, pos: number): number {
  switch (tree.t) {
    case 'char':
      return tree.pos + 1;
    case 'concat':
      return tree.end;
    case 'alt':
      return treeEnd(tree.child, pos);
    case 'star':
      return tree.end;
    case 'group':
      return tree.end;
  }
}

export interface OracleMatch {
  index: number;
  match: string;
  groups: (string | undefined)[];
}

/** 独立的选取规则：最左起点；同起点先比整体长度，再按组序比各组长度（未参与 = -1）；
 *  全部并列时按组序比较区间，起点靠后者胜，其次终点靠后者胜。 */
export function oracleExec(ast: Ast, groupCount: number, input: string): OracleMatch | null {
  for (let start = 0; start <= input.length; start++) {
    const candidates = genTrees(ast, input, start).map(evalTree);
    if (candidates.length === 0) continue;
    let best = candidates[0]!;
    for (let i = 1; i < candidates.length; i++) {
      const c = candidates[i]!;
      if (beats(c, best, groupCount)) best = c;
    }
    const groups: (string | undefined)[] = [];
    for (let g = 1; g <= groupCount; g++) {
      const span = best.caps.get(g);
      groups.push(span === undefined ? undefined : input.slice(span[0], span[1]));
    }
    return { index: start, match: input.slice(start, best.end), groups };
  }
  return null;
}

function beats(c: EvalResult, best: EvalResult, groupCount: number): boolean {
  if (c.end !== best.end) return c.end > best.end;
  for (let g = 1; g <= groupCount; g++) {
    const sc = c.caps.get(g);
    const sb = best.caps.get(g);
    const lc = sc === undefined ? -1 : sc[1] - sc[0];
    const lb = sb === undefined ? -1 : sb[1] - sb[0];
    if (lc !== lb) return lc > lb;
  }
  for (let g = 1; g <= groupCount; g++) {
    const sc = c.caps.get(g);
    const sb = best.caps.get(g);
    if (sc === undefined || sb === undefined) continue;
    if (sc[0] !== sb[0]) return sc[0] > sb[0];
    if (sc[1] !== sb[1]) return sc[1] > sb[1];
  }
  return false;
}
