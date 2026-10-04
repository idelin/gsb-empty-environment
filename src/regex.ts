/**
 * interval-regex：带捕获区间的正则匹配库。
 *
 * 支持语法：字面量、反斜杠转义、分组 `(...)`、并集 `|`、连接、星号 `*`。
 * 优先级：`*` > 连接 > `|`；`*` 绑定左侧最小原子（字面量或分组），可叠加（`a**`）。
 *
 * 匹配语义（自定义，类 POSIX 最左最长）：
 *  1. 最左起点：从位置 0 起找第一个存在匹配的起始位置；
 *  2. 最长整体：在该起点下取整体匹配最长的路径；
 *  3. 组间按组号顺序依次裁决：参与的组优先于未参与的组；
 *     都参与时捕获更长的优先；长度相同取起点更靠前者（仅为确定性兜底）。
 *  4. 星号体内含的捕获组在每一轮迭代开始时重置（取最后一轮的捕获，
 *     与 ECMAScript 量化组行为一致）；未参与（null）与空捕获（[i,i]）严格区分。
 *  5. 星号体若匹配空串，则该轮计入捕获后终止星号展开，杜绝零长度循环。
 */

export type Ast =
  | { kind: "empty" }
  | { kind: "char"; ch: string }
  | { kind: "concat"; parts: Ast[] }
  | { kind: "alt"; options: Ast[] }
  | { kind: "star"; child: Ast; reset?: number[] }
  | { kind: "group"; index: number; child: Ast };

export interface Span {
  start: number;
  end: number;
}

export interface MatchResult {
  /** 整体匹配区间 [start, end)。 */
  start: number;
  end: number;
  /** groups[i] 为第 i+1 组的捕获区间；null 表示该组未参与匹配。 */
  groups: (Span | null)[];
}

export interface Regex {
  readonly pattern: string;
  readonly groupCount: number;
  exec(text: string): MatchResult | null;
}

export class RegexSyntaxError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RegexSyntaxError";
  }
}

// ---------------------------------------------------------------------------
// 解析
// ---------------------------------------------------------------------------

export function parse(pattern: string): { ast: Ast; groupCount: number } {
  let pos = 0;
  let groupCount = 0;

  function peek(): string | undefined {
    return pos < pattern.length ? pattern[pos] : undefined;
  }

  function parseAlt(): Ast {
    const options: Ast[] = [parseConcat()];
    while (peek() === "|") {
      pos++;
      options.push(parseConcat());
    }
    return options.length === 1 ? options[0] : { kind: "alt", options };
  }

  function parseConcat(): Ast {
    const parts: Ast[] = [];
    while (pos < pattern.length && peek() !== ")" && peek() !== "|") {
      parts.push(parseRepeat());
    }
    if (parts.length === 0) return { kind: "empty" };
    if (parts.length === 1) return parts[0];
    return { kind: "concat", parts };
  }

  function parseRepeat(): Ast {
    let node = parseAtom();
    while (peek() === "*") {
      pos++;
      node = { kind: "star", child: node };
    }
    return node;
  }

  function parseAtom(): Ast {
    const ch = pattern[pos];
    if (ch === "(") {
      pos++;
      const index = ++groupCount;
      const child = parseAlt();
      if (peek() !== ")") {
        throw new RegexSyntaxError(`unclosed '(' at offset ${pos}`);
      }
      pos++;
      return { kind: "group", index, child };
    }
    if (ch === "\\") {
      pos++;
      if (pos >= pattern.length) {
        throw new RegexSyntaxError("trailing backslash");
      }
      const lit = pattern[pos];
      pos++;
      return { kind: "char", ch: lit };
    }
    if (ch === "*") {
      throw new RegexSyntaxError(`nothing to repeat at offset ${pos}`);
    }
    if (ch === undefined || ch === ")") {
      throw new RegexSyntaxError(`unexpected '${ch}' at offset ${pos}`);
    }
    pos++;
    return { kind: "char", ch };
  }

  const ast = parseAlt();
  if (pos !== pattern.length) {
    throw new RegexSyntaxError(`unmatched ')' at offset ${pos}`);
  }
  return { ast, groupCount };
}

// ---------------------------------------------------------------------------
// 匹配
// ---------------------------------------------------------------------------

type Caps = (Span | null)[]; // caps[i] 对应第 i+1 组

interface State {
  pos: number;
  caps: Caps;
}

function collectGroupIndices(node: Ast, into: number[]): number[] {
  switch (node.kind) {
    case "group":
      into.push(node.index);
      collectGroupIndices(node.child, into);
      break;
    case "concat":
      for (const part of node.parts) collectGroupIndices(part, into);
      break;
    case "alt":
      for (const option of node.options) collectGroupIndices(option, into);
      break;
    case "star":
      collectGroupIndices(node.child, into);
      break;
    default:
      break;
  }
  return into;
}

/** 预处理：为每个星号节点缓存其体内包含的组号（迭代开始时需要重置）。 */
function annotate(node: Ast): void {
  switch (node.kind) {
    case "star":
      node.reset = collectGroupIndices(node.child, []);
      annotate(node.child);
      break;
    case "concat":
      node.parts.forEach(annotate);
      break;
    case "alt":
      node.options.forEach(annotate);
      break;
    case "group":
      annotate(node.child);
      break;
    default:
      break;
  }
}

function cleared(caps: Caps, ids: number[]): Caps {
  const next = caps.slice();
  for (const id of ids) next[id - 1] = null;
  return next;
}

/**
 * 枚举 node 在 text[pos..] 上的全部匹配路径（含全部捕获组合）。
 * caps 按持久化方式使用（只复制不原地修改），路径之间互不影响。
 */
function* enumMatch(node: Ast, text: string, pos: number, caps: Caps): Generator<State> {
  switch (node.kind) {
    case "empty":
      yield { pos, caps };
      break;
    case "char":
      if (pos < text.length && text[pos] === node.ch) {
        yield { pos: pos + 1, caps };
      }
      break;
    case "concat": {
      let states: State[] = [{ pos, caps }];
      for (const part of node.parts) {
        const next: State[] = [];
        for (const st of states) {
          for (const r of enumMatch(part, text, st.pos, st.caps)) {
            next.push(r);
          }
        }
        states = next;
        if (states.length === 0) break;
      }
      yield* states;
      break;
    }
    case "alt":
      for (const option of node.options) {
        yield* enumMatch(option, text, pos, caps);
      }
      break;
    case "group":
      for (const r of enumMatch(node.child, text, pos, caps)) {
        const next = r.caps.slice();
        next[node.index - 1] = { start: pos, end: r.pos };
        yield { pos: r.pos, caps: next };
      }
      break;
    case "star":
      yield* enumStar(node, text, pos, caps);
      break;
  }
}

function* enumStar(
  node: { kind: "star"; child: Ast; reset?: number[] },
  text: string,
  pos: number,
  caps: Caps,
): Generator<State> {
  // 零次迭代：体内各组保持进入星号时的状态（通常为未参与）。
  yield { pos, caps };
  const reset = node.reset ?? collectGroupIndices(node.child, []);
  // 每一轮迭代开始时重置体内捕获组（最后一轮生效）。
  for (const r of enumMatch(node.child, text, pos, cleared(caps, reset))) {
    if (r.pos === pos) {
      // 空迭代：计入本轮捕获后立即停止展开，避免零长度循环。
      yield r;
    } else {
      yield* enumStar(node, text, r.pos, r.caps);
    }
  }
}

/**
 * 路径比较：返回负数表示 a 更优。
 * 先比整体长度，再按组号顺序比较：参与 > 未参与，长 > 短，同长取起点靠前者。
 */
function compareStates(a: State, b: State, groupCount: number): number {
  if (a.pos !== b.pos) return b.pos - a.pos;
  for (let g = 0; g < groupCount; g++) {
    const pa = a.caps[g];
    const pb = b.caps[g];
    if (pa === null && pb === null) continue;
    if (pa === null) return 1;
    if (pb === null) return -1;
    const la = pa.end - pa.start;
    const lb = pb.end - pb.start;
    if (la !== lb) return lb - la;
    if (pa.start !== pb.start) return pa.start - pb.start;
  }
  return 0;
}

export function compile(pattern: string): Regex {
  const { ast, groupCount } = parse(pattern);
  annotate(ast);
  return {
    pattern,
    groupCount,
    exec(text: string): MatchResult | null {
      for (let start = 0; start <= text.length; start++) {
        const caps: Caps = new Array<Span | null>(groupCount).fill(null);
        let best: State | null = null;
        for (const st of enumMatch(ast, text, start, caps)) {
          if (best === null || compareStates(st, best, groupCount) < 0) {
            best = st;
          }
        }
        if (best !== null) {
          return {
            start,
            end: best.pos,
            groups: best.caps.map((c) => (c === null ? null : { start: c.start, end: c.end })),
          };
        }
      }
      return null;
    },
  };
}

export function exec(pattern: string, text: string): MatchResult | null {
  return compile(pattern).exec(text);
}
