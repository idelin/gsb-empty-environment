import type { RegexNode } from './types.ts';

/**
 * Thompson 构造的 ε-NFA。
 *
 * 每个片段都有单一入口与单一出口；星号通过从出口回到入口的 ε 边实现，
 * 因此 NFA 中的有向环只可能来自星号节点，找到的歧义环天然“可泵送”。
 */

export interface EpsilonEdge {
  readonly to: number;
  /** 边的唯一编号，用于区分同源同目标的不同 ε 路径（Thompson 构造会产生平行边）。 */
  readonly edgeId: number;
}

export interface NfaState {
  /** 字符转移：同一字符可能指向多个目标（歧义的来源之一）。 */
  readonly transitions: Map<string, number[]>;
  readonly epsilon: EpsilonEdge[];
}

export interface Nfa {
  readonly states: NfaState[];
  readonly start: number;
  readonly accept: number;
  /** 模式中出现过的所有字面量字符。 */
  readonly alphabet: ReadonlySet<string>;
}

interface Fragment {
  readonly start: number;
  readonly accept: number;
}

export class NfaBuilder {
  private readonly states: NfaState[] = [];
  private edgeCounter = 0;
  private readonly alphabet = new Set<string>();

  build(root: RegexNode): Nfa {
    const fragment = this.buildNode(root);
    return {
      states: this.states,
      start: fragment.start,
      accept: fragment.accept,
      alphabet: this.alphabet,
    };
  }

  private newState(): number {
    const id = this.states.length;
    this.states.push({ transitions: new Map(), epsilon: [] });
    return id;
  }

  private addEpsilon(from: number, to: number): void {
    this.states[from].epsilon.push({ to, edgeId: this.edgeCounter++ });
  }

  private addTransition(from: number, char: string, to: number): void {
    const bucket = this.states[from].transitions.get(char);
    if (bucket === undefined) {
      this.states[from].transitions.set(char, [to]);
    } else {
      bucket.push(to);
    }
    this.alphabet.add(char);
  }

  private buildNode(node: RegexNode): Fragment {
    switch (node.type) {
      case 'literal': {
        const start = this.newState();
        const accept = this.newState();
        this.addTransition(start, node.char, accept);
        return { start, accept };
      }

      case 'group': {
        return this.buildNode(node.child);
      }

      case 'concat': {
        let current = this.buildNode(node.parts[0]);
        for (let i = 1; i < node.parts.length; i += 1) {
          const next = this.buildNode(node.parts[i]);
          this.addEpsilon(current.accept, next.start);
          current = { start: current.start, accept: next.accept };
        }
        return current;
      }

      case 'alt': {
        const entry = this.newState();
        const exit = this.newState();
        for (const option of node.options) {
          const fragment = this.buildNode(option);
          this.addEpsilon(entry, fragment.start);
          this.addEpsilon(fragment.accept, exit);
        }
        return { start: entry, accept: exit };
      }

      case 'star': {
        const entry = this.newState();
        const exit = this.newState();
        const body = this.buildNode(node.child);
        this.addEpsilon(entry, body.start);
        this.addEpsilon(entry, exit);
        this.addEpsilon(body.accept, body.start);
        this.addEpsilon(body.accept, exit);
        return { start: entry, accept: exit };
      }
    }
  }
}

export function buildNfa(root: RegexNode): Nfa {
  return new NfaBuilder().build(root);
}

/** 计算一个状态集合的 ε-闭包（沿任意 ε 边可达的全部状态）。 */
export function epsilonClosure(nfa: Nfa, seeds: readonly number[]): Set<number> {
  const closure = new Set<number>(seeds);
  const queue = [...seeds];

  while (queue.length > 0) {
    const state = queue.pop()!;
    for (const edge of nfa.states[state].epsilon) {
      if (!closure.has(edge.to)) {
        closure.add(edge.to);
        queue.push(edge.to);
      }
    }
  }

  return closure;
}

/** 从某个状态消费一个字符后，再取 ε-闭包，得到可达状态集合。 */
export function move(nfa: Nfa, states: ReadonlySet<number>, char: string): Set<number> {
  const destinations: number[] = [];
  for (const state of states) {
    const targets = nfa.states[state].transitions.get(char);
    if (targets !== undefined) {
      destinations.push(...targets);
    }
  }
  return epsilonClosure(nfa, destinations);
}

/** 整串匹配语义：输入耗尽后 NFA 是否落在接受状态。 */
export function nfaAccepts(nfa: Nfa, input: string): boolean {
  let current = epsilonClosure(nfa, [nfa.start]);
  for (const char of input) {
    current = move(nfa, current, char);
    if (current.size === 0) {
      return false;
    }
  }
  return current.has(nfa.accept);
}

/**
 * 预计算可沿 ε 边与字符边到达接受状态的全部状态。
 * 用于在乘积图中只保留“前缀仍有希望被接受”的状态对。
 */
export function statesThatCanReachAccept(nfa: Nfa): Set<number> {
  const reverse = new Map<number, number[]>();
  const ensureBucket = (state: number): number[] => {
    let bucket = reverse.get(state);
    if (bucket === undefined) {
      bucket = [];
      reverse.set(state, bucket);
    }
    return bucket;
  };

  for (let state = 0; state < nfa.states.length; state += 1) {
    for (const edge of nfa.states[state].epsilon) {
      ensureBucket(edge.to).push(state);
    }
    for (const targets of nfa.states[state].transitions.values()) {
      for (const target of targets) {
        ensureBucket(target).push(state);
      }
    }
  }

  const canReach = new Set<number>([nfa.accept]);
  const queue = [nfa.accept];
  while (queue.length > 0) {
    const state = queue.pop()!;
    for (const predecessor of reverse.get(state) ?? []) {
      if (!canReach.has(predecessor)) {
        canReach.add(predecessor);
        queue.push(predecessor);
      }
    }
  }
  return canReach;
}

/**
 * 在 NFA 上寻找从给定状态到任意目标集合的一条具体路径，
 * 返回路径上消费的字符序列（用于还原见证的前缀与泵段）。
 * BFS 保证找到的是较短路径之一。
 */
export function findConsumedPath(
  nfa: Nfa,
  from: number,
  targets: ReadonlySet<number>,
  budget = 50_000,
): string | null {
  interface Frame {
    readonly state: number;
    readonly label: string;
  }

  const visited = new Set<number>([from]);
  const queue: Frame[] = [{ state: from, label: '' }];
  let explored = 0;

  while (queue.length > 0) {
    const frame = queue.shift()!;
    explored += 1;
    if (explored > budget) {
      return null;
    }
    if (targets.has(frame.state)) {
      return frame.label;
    }

    for (const edge of nfa.states[frame.state].epsilon) {
      if (!visited.has(edge.to)) {
        visited.add(edge.to);
        queue.push({ state: edge.to, label: frame.label });
      }
    }
    for (const [char, targets2] of nfa.states[frame.state].transitions) {
      for (const target of targets2) {
        if (!visited.has(target)) {
          visited.add(target);
          queue.push({ state: target, label: frame.label + char });
        }
      }
    }
  }

  return null;
}
