/**
 * 独立参考实现：用于交叉核对 src/regex.ts。
 *
 * 与库的实现路径完全不同：
 *  1. 自带一个最小解析器（二叉 AST）；
 *  2. 把每个星号有界展开为“重复 0..K 次”的并集（K = 文本长度 + 1），
 *     每份拷贝前插入显式的组重置节点 clr，得到无星语法树——
 *     展开上界成立的理由：消耗字符的迭代至多 len 次；空迭代只有最后一轮
 *     可能影响最终捕获（更早的空迭代会被后续迭代覆盖），故至多再 +1 次；
 *  3. 对无星语法树做状态集模拟（每个节点把输入状态集映射为输出状态集，
 *     逐步去重），穷举全部匹配路径；
 *  4. 按同一套优先级规则（最左、最长整体、按组序最长捕获）选优。
 *
 * 全程不调用宿主语言（JS）的正则引擎。
 */

type RNode =
  | { t: "empty" }
  | { t: "char"; c: string }
  | { t: "cat"; l: RNode; r: RNode }
  | { t: "or"; l: RNode; r: RNode }
  | { t: "grp"; i: number; x: RNode }
  | { t: "rep"; x: RNode }
  | { t: "clr"; ids: number[]; x: RNode };

export type RefSpan = [number, number];

export interface RefResult {
  start: number;
  end: number;
  groups: (RefSpan | null)[];
}

function parseRef(src: string): { root: RNode; count: number } {
  let pos = 0;
  let count = 0;
  const peek = (): string => (pos < src.length ? src[pos] : "");

  function parseAlt(): RNode {
    let left = parseCat();
    while (peek() === "|") {
      pos++;
      left = { t: "or", l: left, r: parseCat() };
    }
    return left;
  }

  function parseCat(): RNode {
    let node: RNode = { t: "empty" };
    let has = false;
    while (pos < src.length && peek() !== "|" && peek() !== ")") {
      const atom = parseRep();
      node = has ? { t: "cat", l: node, r: atom } : atom;
      has = true;
    }
    return node;
  }

  function parseRep(): RNode {
    let node = parseAtom();
    while (peek() === "*") {
      pos++;
      node = { t: "rep", x: node };
    }
    return node;
  }

  function parseAtom(): RNode {
    const c = src[pos];
    if (c === "(") {
      pos++;
      const i = ++count;
      const x = parseAlt();
      if (peek() !== ")") throw new Error(`unclosed '(' at ${pos}`);
      pos++;
      return { t: "grp", i, x };
    }
    if (c === "\\") {
      pos++;
      if (pos >= src.length) throw new Error("trailing backslash");
      const lit = src[pos];
      pos++;
      return { t: "char", c: lit };
    }
    if (c === "*" || c === undefined || c === ")") {
      throw new Error(`unexpected '${c}' at ${pos}`);
    }
    pos++;
    return { t: "char", c };
  }

  const root = parseAlt();
  if (pos !== src.length) throw new Error(`unmatched ')' at ${pos}`);
  return { root, count };
}

function collectIds(node: RNode, into: number[]): void {
  switch (node.t) {
    case "grp":
      into.push(node.i);
      collectIds(node.x, into);
      break;
    case "cat":
    case "or":
      collectIds(node.l, into);
      collectIds(node.r, into);
      break;
    case "rep":
    case "clr":
      collectIds(node.x, into);
      break;
    default:
      break;
  }
}

/** 把星号展开成 0..k 次重复的并集，并在每份拷贝前重置体内捕获组。 */
function unroll(node: RNode, k: number): RNode {
  switch (node.t) {
    case "rep": {
      const ids: number[] = [];
      collectIds(node.x, ids);
      const body = unroll(node.x, k);
      let acc: RNode = { t: "empty" };
      let result: RNode = { t: "empty" };
      for (let i = 0; i < k; i++) {
        acc = { t: "cat", l: acc, r: { t: "clr", ids, x: body } };
        result = { t: "or", l: result, r: acc };
      }
      return result;
    }
    case "cat":
      return { t: "cat", l: unroll(node.l, k), r: unroll(node.r, k) };
    case "or":
      return { t: "or", l: unroll(node.l, k), r: unroll(node.r, k) };
    case "grp":
      return { t: "grp", i: node.i, x: unroll(node.x, k) };
    default:
      return node;
  }
}

type RCaps = (RefSpan | null)[]; // 1 起始下标，0 号位闲置

interface RState {
  pos: number;
  caps: RCaps;
}

function stateKey(s: RState): string {
  let out = String(s.pos);
  for (const c of s.caps) out += c === null ? "|-" : `|${c[0]},${c[1]}`;
  return out;
}

function dedupe(states: RState[]): RState[] {
  const seen = new Map<string, RState>();
  for (const s of states) {
    const k = stateKey(s);
    if (!seen.has(k)) seen.set(k, s);
  }
  return [...seen.values()];
}

/** 无星语法树的状态集模拟：把输入状态集映射为输出状态集。 */
function sim(node: RNode, text: string, states: RState[]): RState[] {
  if (states.length === 0) return states;
  switch (node.t) {
    case "empty":
      return states;
    case "char": {
      const out: RState[] = [];
      for (const s of states) {
        if (s.pos < text.length && text[s.pos] === node.c) {
          out.push({ pos: s.pos + 1, caps: s.caps });
        }
      }
      return dedupe(out);
    }
    case "cat":
      return sim(node.r, text, sim(node.l, text, states));
    case "or":
      return dedupe([...sim(node.l, text, states), ...sim(node.r, text, states)]);
    case "clr":
      return sim(
        node.x,
        text,
        states.map((s) => ({
          pos: s.pos,
          caps: s.caps.map((c, idx) => (node.ids.indexOf(idx) >= 0 ? null : c)),
        })),
      );
    case "grp": {
      const out: RState[] = [];
      for (const s of states) {
        for (const r of sim(node.x, text, [s])) {
          const caps = r.caps.slice();
          caps[node.i] = [s.pos, r.pos];
          out.push({ pos: r.pos, caps });
        }
      }
      return dedupe(out);
    }
    case "rep":
      throw new Error("star must be unrolled before simulation");
  }
}

function better(a: RState, b: RState, count: number): boolean {
  if (a.pos !== b.pos) return a.pos > b.pos;
  for (let g = 1; g <= count; g++) {
    const x = a.caps[g];
    const y = b.caps[g];
    if (x === null && y === null) continue;
    if (x === null) return false;
    if (y === null) return true;
    const lx = x[1] - x[0];
    const ly = y[1] - y[0];
    if (lx !== ly) return lx > ly;
    if (x[0] !== y[0]) return x[0] < y[0];
  }
  return false;
}

export function refExec(pattern: string, text: string): RefResult | null {
  const { root, count } = parseRef(pattern);
  const tree = unroll(root, text.length + 1);
  for (let start = 0; start <= text.length; start++) {
    const init: RState = { pos: start, caps: new Array<RefSpan | null>(count + 1).fill(null) };
    const states = sim(tree, text, [init]);
    if (states.length === 0) continue;
    let best = states[0];
    for (const s of states) {
      if (better(s, best, count)) best = s;
    }
    return { start, end: best.pos, groups: best.caps.slice(1) };
  }
  return null;
}
