/**
 * 子节点序列的正规模式（Regular Pattern）。
 *
 * 树文法中，每条产生式  label -> Pattern  描述该标签节点的
 * 合法子节点序列（等价于 DTD 内容模型 / 树自动机的水平正则转移）。
 */

export type Pattern =
  | { kind: 'lit'; label: string }        // 匹配一个指定标签的子节点
  | { kind: 'seq'; parts: Pattern[] }     // 顺序连接
  | { kind: 'alt'; options: Pattern[] }   // 选择（并）
  | { kind: 'opt'; inner: Pattern }       // 0 或 1 次
  | { kind: 'star'; inner: Pattern }      // 0 或多次
  | { kind: 'plus'; inner: Pattern }      // 1 或多次
  | { kind: 'empty' }                     // 空序列
  | { kind: 'text' };                     // 文本叶子（不允许元素子节点）

export const lit = (label: string): Pattern => ({ kind: 'lit', label });
export const seq = (...parts: Pattern[]): Pattern => ({ kind: 'seq', parts });
export const alt = (...options: Pattern[]): Pattern => ({ kind: 'alt', options });
export const opt = (inner: Pattern): Pattern => ({ kind: 'opt', inner });
export const star = (inner: Pattern): Pattern => ({ kind: 'star', inner });
export const plus = (inner: Pattern): Pattern => ({ kind: 'plus', inner });
export const empty = (): Pattern => ({ kind: 'empty' });
export const text = (): Pattern => ({ kind: 'text' });

/** 将模式渲染为紧凑可读的形式，用于诊断信息。 */
export function describePattern(p: Pattern): string {
  switch (p.kind) {
    case 'lit': return p.label;
    case 'seq': return p.parts.map(describePattern).join(', ');
    case 'alt': return p.options.map(describePattern).join(' | ');
    case 'opt': return `${describeAtom(p.inner)}?`;
    case 'star': return `${describeAtom(p.inner)}*`;
    case 'plus': return `${describeAtom(p.inner)}+`;
    case 'empty': return 'ε';
    case 'text': return '#text';
  }
}

function describeAtom(p: Pattern): string {
  return p.kind === 'seq' || p.kind === 'alt'
    ? `(${describePattern(p)})`
    : describePattern(p);
}

/* ------------------------------------------------------------------ */
/* 模式 -> NFA（Thompson 构造）。NFA 即树自动机在某节点处的            */
/* “水平自动机”：状态集合 + 以子节点标签为字母表的转移。               */
/* ------------------------------------------------------------------ */

export interface NFA {
  start: number;
  accept: number;
  /** transitions[from] = [{ label, to }]，label 为 null 表示 ε 转移 */
  transitions: Map<number, { label: string | null; to: number }[]>;
}

export function compilePattern(p: Pattern): NFA {
  const transitions = new Map<number, { label: string | null; to: number }[]>();
  let nextState = 0;
  const newState = () => nextState++;
  const addTrans = (from: number, label: string | null, to: number) => {
    if (!transitions.has(from)) transitions.set(from, []);
    transitions.get(from)!.push({ label, to });
  };

  const build = (pat: Pattern): { start: number; accept: number } => {
    switch (pat.kind) {
      case 'lit': {
        const s = newState(), a = newState();
        addTrans(s, pat.label, a);
        return { start: s, accept: a };
      }
      case 'empty':
      case 'text': {
        const s = newState(), a = newState();
        addTrans(s, null, a);
        return { start: s, accept: a };
      }
      case 'seq': {
        if (pat.parts.length === 0) return build({ kind: 'empty' });
        const frags = pat.parts.map(build);
        for (let i = 0; i < frags.length - 1; i++) {
          addTrans(frags[i].accept, null, frags[i + 1].start);
        }
        return { start: frags[0].start, accept: frags[frags.length - 1].accept };
      }
      case 'alt': {
        const s = newState(), a = newState();
        for (const opt of pat.options) {
          const f = build(opt);
          addTrans(s, null, f.start);
          addTrans(f.accept, null, a);
        }
        return { start: s, accept: a };
      }
      case 'opt': {
        const s = newState(), a = newState();
        const f = build(pat.inner);
        addTrans(s, null, f.start);
        addTrans(s, null, a);
        addTrans(f.accept, null, a);
        return { start: s, accept: a };
      }
      case 'star': {
        const s = newState(), a = newState();
        const f = build(pat.inner);
        addTrans(s, null, f.start);
        addTrans(s, null, a);
        addTrans(f.accept, null, f.start);
        addTrans(f.accept, null, a);
        return { start: s, accept: a };
      }
      case 'plus': {
        const f = build(pat.inner);
        const a = newState();
        addTrans(f.accept, null, f.start);
        addTrans(f.accept, null, a);
        return { start: f.start, accept: a };
      }
    }
  };

  const { start, accept } = build(p);
  return { start, accept, transitions };
}

/* ------------------------------------------------------------------ */
/* NFA 模拟 + 失败分析                                                 */
/* ------------------------------------------------------------------ */

export interface MatchFailure {
  /** 失败位置：无法消费的子节点下标；等于 labels.length 表示在末尾缺元素 */
  position: number;
  /** 失败位置处，自动机期望看到的标签集合 */
  expected: string[];
  /** 从当前状态出发、再消费若干期望标签之后才可能出现的标签（用于判断“缺失”而非“多余”） */
  future: string[];
}

export type MatchResult = { ok: true } | { ok: false; failure: MatchFailure };

function epsilonClosure(nfa: NFA, states: Set<number>): Set<number> {
  const stack = [...states];
  const result = new Set(states);
  while (stack.length > 0) {
    const s = stack.pop()!;
    for (const t of nfa.transitions.get(s) ?? []) {
      if (t.label === null && !result.has(t.to)) {
        result.add(t.to);
        stack.push(t.to);
      }
    }
  }
  return result;
}

function outgoingLabels(nfa: NFA, states: Set<number>): string[] {
  const labels = new Set<string>();
  for (const s of states) {
    for (const t of nfa.transitions.get(s) ?? []) {
      if (t.label !== null) labels.add(t.label);
    }
  }
  return [...labels];
}

/** 从 states 出发，经过 >=1 条标签转移后可到达状态的所有出边标签（含第一步）。 */
function futureLabels(nfa: NFA, states: Set<number>): string[] {
  const seen = new Set<number>();
  const labels = new Set<string>();
  let frontier = epsilonClosure(nfa, states);
  while (frontier.size > 0) {
    const next = new Set<number>();
    for (const s of frontier) {
      if (seen.has(s)) continue;
      seen.add(s);
      for (const t of nfa.transitions.get(s) ?? []) {
        if (t.label !== null) {
          labels.add(t.label);
          next.add(t.to);
        }
      }
    }
    frontier = epsilonClosure(nfa, next);
  }
  return [...labels];
}

/**
 * 用 NFA 匹配子节点标签序列。
 * 失败时返回最早失败位置及期望/后续标签，供生成精细诊断。
 */
export function matchLabels(nfa: NFA, labels: string[]): MatchResult {
  let current = epsilonClosure(nfa, new Set([nfa.start]));
  for (let i = 0; i < labels.length; i++) {
    const next = new Set<number>();
    for (const s of current) {
      for (const t of nfa.transitions.get(s) ?? []) {
        if (t.label === labels[i]) next.add(t.to);
      }
    }
    if (next.size === 0) {
      return {
        ok: false,
        failure: {
          position: i,
          expected: outgoingLabels(nfa, current),
          future: futureLabels(nfa, current),
        },
      };
    }
    current = epsilonClosure(nfa, next);
  }
  if (current.has(nfa.accept)) return { ok: true };
  return {
    ok: false,
    failure: {
      position: labels.length,
      expected: outgoingLabels(nfa, current),
      future: futureLabels(nfa, current),
    },
  };
}
