import { buildNfa, epsilonClosure, statesThatCanReachAccept } from './nfa.ts';
import type { Nfa } from './nfa.ts';
import type { ParsedPattern } from './types.ts';

/**
 * 路径敏感的歧义环检测（边身份乘积自动机 + 三色 DFS）。
 *
 * 朴素“状态对”乘积图的问题：同一条 NFA 路径在 ε 边前后处于不同状态，
 * 会被误判成两条路径，把 `a*`、`(a|b)*` 这类唯一解析模式报成指数回溯。
 *
 * 本实现给乘积状态的每条腿记录 (state, lastEdge)：
 *   - state：当前 NFA 状态；
 *   - lastEdge：到达该状态所走的最后一条边 ID（起点为 null）。
 *
 * 转移规则：
 *   - 独立 ε 移动：一条腿沿自己的 ε 边前进、另一条原地等待，并更新 lastEdge；
 *   - 字符移动：两条腿同步消费同一个字符，消费后保留各自闭包入口的 lastEdge，
 *     以免两条不同位置的同名字面量边被错误地当成同一条边。
*
* 歧义 twin state：两条腿到达同一 NFA 状态，但 lastEdge 不同。这意味着同一
 * 输入字符串存在两条“最后一步都不同”的解析路径。lastEdge 相同（含同为起点
 * null）时只代表同一条路径，不算歧义。
 *
 * 环上出现过歧义 twin 且至少消费一个字符时，环上字符即可泵送；Thompson NFA
 * 的有向环只来自星号，泵送会成倍放大回溯匹配器需要枚举的解析路径。
 */

export interface AmbiguityCycle {
  /** 进入歧义环之前需要消费的前缀字符串。 */
  readonly prefix: string;
  /** 环本身消费的字符串，即重复段。 */
  readonly pump: string;
}

interface Leg {
  readonly state: number;
  readonly lastEdge: number | null;
}

interface ProductEdge {
  readonly toKey: string;
  readonly toA: Leg;
  readonly toB: Leg;
  readonly label: string;
  readonly consuming: boolean;
}

const DEAD_CHAR_CANDIDATES = [
  '~', '`', '@', '#', '%', '&', '_', '=', '+', ':', ';', '<', '>',
  '\u0000', '\uE000',
];

export function chooseDeadChar(alphabet: ReadonlySet<string>): string {
  for (const candidate of DEAD_CHAR_CANDIDATES) {
    if (!alphabet.has(candidate)) {
      return candidate;
    }
  }
  for (let codePoint = 0xe001; codePoint <= 0xf8ff; codePoint += 1) {
    const char = String.fromCodePoint(codePoint);
    if (!alphabet.has(char)) {
      return char;
    }
  }
  throw new Error('无法选取死字符：模式字母表异常庞大');
}

function legKey(leg: Leg): string {
  return `${leg.state}:${leg.lastEdge === null ? '-' : leg.lastEdge}`;
}

export class AmbiguityDetector {
  private readonly nfa: Nfa;
  private readonly canReachAccept: ReadonlySet<number>;
  private readonly edgeCache = new Map<string, ProductEdge[]>();
  private readonly budget: number;
  private visitedCount = 0;

  constructor(parsed: ParsedPattern, budget = 200_000) {
    this.nfa = buildNfa(parsed.ast);
    this.canReachAccept = statesThatCanReachAccept(this.nfa);
    this.budget = budget;
  }

  getNfa(): Nfa {
    return this.nfa;
  }

  /**
   * 搜索可泵送歧义环。找不到（或预算耗尽且未找到）时返回 null。
   * 调用方必须把 null 解释为“未证实”，而不是“无歧义/安全”。
   */
  findAmbiguityCycle(): AmbiguityCycle | null {
    // 第一阶段：枚举可达乘积状态，记录全部歧义 twin 与每个状态的见证串。
    const startLeg: Leg = { state: this.nfa.start, lastEdge: null };
    const startKey = this.productKey(startLeg, startLeg);
    const discovered = new Map<string, { legs: [Leg, Leg]; label: string }>();
    const queue: Array<{ key: string; a: Leg; b: Leg; label: string }> = [
      { key: startKey, a: startLeg, b: startLeg, label: '' },
    ];
    discovered.set(startKey, { legs: [startLeg, startLeg], label: '' });

    while (queue.length > 0) {
      const current = queue.shift()!;
      this.visitedCount += 1;
      if (this.visitedCount > this.budget) {
        return null;
      }
      for (const edge of this.edgesFor(current.a, current.b)) {
        if (discovered.has(edge.toKey)) {
          continue;
        }
        const label = current.label + edge.label;
        discovered.set(edge.toKey, {
          legs: [edge.toA, edge.toB],
          label,
        });
        queue.push({ key: edge.toKey, a: edge.toA, b: edge.toB, label });
      }
    }

    // 第二阶段：在歧义 twin 之间寻找“消费至少一个字符”的环。
    for (const [twinKey, info] of discovered) {
      if (!this.isAmbiguousTwin(info.legs[0], info.legs[1])) {
        continue;
      }
      const cycle = this.searchCycleFromTwin(twinKey, info.legs[0], info.legs[1], discovered);
      if (cycle !== null) {
        if (this.isReachableWitness(info.label, info.legs[0].state)) {
          return { prefix: info.label, pump: cycle };
        }
      }
    }
    return null;
  }

  /**
   * 从某个歧义 twin 出发做 DFS，寻找一条回到任一歧义 twin、且沿途消费过字符的环。
   * 返回环上消费的字符串（泵）；找不到返回 null。
   */
  private searchCycleFromTwin(
    twinKey: string,
    a: Leg,
    b: Leg,
    discovered: ReadonlyMap<string, { legs: [Leg, Leg]; label: string }>,
  ): string | null {
    const isTwin = (key: string): boolean => {
      const info = discovered.get(key);
      return info !== undefined && this.isAmbiguousTwin(info.legs[0], info.legs[1]);
    };

    // 带路径访问集的 DFS：每个栈帧携带自己的已访问集合，
    // 这样通向“另一个 twin”的分支不会被全局 onStack 误杀。
    interface Frame {
      key: string;
      a: Leg;
      b: Leg;
      label: string;
      consuming: boolean;
      visited: ReadonlySet<string>;
    }

    const stack: Frame[] = [
      { key: twinKey, a, b, label: '', consuming: false, visited: new Set([twinKey]) },
    ];

    while (stack.length > 0) {
      const frame = stack.pop()!;
      for (const edge of this.edgesFor(frame.a, frame.b)) {
        if (!discovered.has(edge.toKey)) {
          continue;
        }
        const consuming = frame.consuming || edge.consuming;
        if (isTwin(edge.toKey) && consuming) {
          return frame.label + edge.label;
        }
        if (frame.visited.has(edge.toKey)) {
          continue;
        }
        stack.push({
          key: edge.toKey,
          a: edge.toA,
          b: edge.toB,
          label: frame.label + edge.label,
          consuming,
          visited: new Set(frame.visited).add(edge.toKey),
        });
      }
    }
    return null;
  }

  /**
   * 健全性检查：从 NFA 起点沿 witness 字符前进，结束后必须能到达 twin 所在状态。
   * 只验证“前缀+泵是真实可达路径”，不要求整串命中 accept（泵之后还会接失败后缀）。
   */
  private isReachableWitness(witness: string, twinState: number): boolean {
    let current = epsilonClosure(this.nfa, [this.nfa.start]);
    for (const char of witness) {
      const destinations: number[] = [];
      for (const state of current) {
        const targets = this.nfa.states[state].transitions.get(char);
        if (targets !== undefined) {
          destinations.push(...targets);
        }
      }
      current = epsilonClosure(this.nfa, destinations);
      if (current.size === 0) {
        return false;
      }
    }
    return current.has(twinState);
  }

  /**
   * 歧义 twin：同一 NFA 状态、但最后一条边不同。
   * lastEdge 相同（含同为 null 的起点）只代表同一条路径，不算歧义。
   */
  private isAmbiguousTwin(a: Leg, b: Leg): boolean {
    return a.state === b.state && a.lastEdge !== b.lastEdge;
  }

  private edgesFor(legA: Leg, legB: Leg): ProductEdge[] {
    const key = `${legKey(legA)}#${legKey(legB)}`;
    const cached = this.edgeCache.get(key);
    if (cached !== undefined) {
      return cached;
    }

    const edges: ProductEdge[] = [];
    const seen = new Set<string>();
    const pushEdge = (a: Leg, b: Leg, label: string, consuming: boolean): void => {
      if (!this.canReachAccept.has(a.state) || !this.canReachAccept.has(b.state)) {
        return;
      }
      const toKey = this.productKey(a, b);
      const dedupeKey = `${toKey}|${label}|${consuming ? 1 : 0}`;
      if (seen.has(dedupeKey)) {
        return;
      }
      seen.add(dedupeKey);
      edges.push({ toKey, toA: a, toB: b, label, consuming });
    };

    // 独立 ε 移动：一条腿前进、另一条等待，各自更新 lastEdge。
    for (const edge of this.nfa.states[legA.state].epsilon) {
      pushEdge({ state: edge.to, lastEdge: edge.edgeId }, legB, '', false);
    }
    for (const edge of this.nfa.states[legB.state].epsilon) {
      pushEdge(legA, { state: edge.to, lastEdge: edge.edgeId }, '', false);
    }

    // 同步字符移动：各自经 ε-闭包后消费相同字符，再对可达腿做笛卡尔积。
    const chars = new Set<string>([
      ...this.nfa.states[legA.state].transitions.keys(),
      ...this.nfa.states[legB.state].transitions.keys(),
    ]);
    for (const char of chars) {
      const legsAfterA = this.legsAfterChar(legA, char);
      const legsAfterB = this.legsAfterChar(legB, char);
      if (legsAfterA.length === 0 || legsAfterB.length === 0) {
        continue;
      }
      for (const a of legsAfterA) {
        for (const b of legsAfterB) {
          pushEdge(a, b, char, true);
        }
      }
    }

    this.edgeCache.set(key, edges);
    return edges;
  }

  /**
   * 从某条腿出发，经 ε-闭包后消费字符 char 能到达的状态。
   * 闭包按 (state, lastEdge) 去重，保留经由不同 ε 边到达同一状态的区别。
   * 关键：消费字符后保留“闭包入口”的 lastEdge，而不是用字符边覆盖它——
   * 这样两个同名字面量（如两条不同的 b 边）经由不同分支 ε 边到达时，
   * lastEdge 仍然不同，歧义 twin 不会被同步字符移动抹掉。
   */
  private legsAfterChar(leg: Leg, char: string): Leg[] {
    const closure = this.epsilonClosure(leg);
    const result: Leg[] = [];
    const seen = new Set<number>();

    for (const entry of closure) {
      const targets = this.nfa.states[entry.state].transitions.get(char);
      if (targets === undefined) {
        continue;
      }
      for (const target of targets) {
        if (!seen.has(target)) {
          seen.add(target);
          const next: Leg = { state: target, lastEdge: entry.lastEdge };
          result.push(next);
        }
      }
    }
    return result;
  }

  /** 路径敏感 ε-闭包：保留全部不同的 (state, lastEdge) 组合。 */
  private epsilonClosure(root: Leg): Leg[] {
    const entries: Leg[] = [root];
    const visited = new Set<string>([legKey(root)]);
    const queue: Leg[] = [root];

    while (queue.length > 0) {
      const current = queue.shift()!;
      for (const edge of this.nfa.states[current.state].epsilon) {
        const next: Leg = { state: edge.to, lastEdge: edge.edgeId };
        const key = legKey(next);
        if (visited.has(key)) {
          continue;
        }
        visited.add(key);
        entries.push(next);
        queue.push(next);
      }
    }
    return entries;
  }

  private productKey(a: Leg, b: Leg): string {
    return `${legKey(a)}#${legKey(b)}`;
  }
}

/**
 * 构造失败后缀：重复段本身（可以继续匹配并产生更多解析分支）
 * 拼接一个模式字母表外的死字符（无法被任何分支消化，匹配必然失败）。
 */
export function buildFailingSuffix(
  nfa: Nfa,
  pump: string,
): { suffix: string; deadChar: string } {
  const deadChar = chooseDeadChar(nfa.alphabet);
  return { suffix: pump + deadChar, deadChar };
}

/** 便捷封装：解析后的模式 -> 歧义环 + 死字符见证构件。 */
export function detectAmbiguity(
  parsed: ParsedPattern,
  budget?: number,
): { cycle: AmbiguityCycle; deadChar: string } | null {
  const detector = new AmbiguityDetector(parsed, budget);
  const cycle = detector.findAmbiguityCycle();
  if (cycle === null) {
    return null;
  }
  const { deadChar } = buildFailingSuffix(detector.getNfa(), cycle.pump);
  return { cycle, deadChar };
}
