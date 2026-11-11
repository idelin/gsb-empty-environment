import { parsePattern } from './parser.ts';
import type {
  ParsedPattern,
  RegexNode,
} from './types.ts';
import { StepCapExceededError } from './types.ts';

/**
 * 教学用回溯匹配解释器。
 *
 * 语义刻意模仿经典回溯正则引擎：对每个并集分支、星号的“继续/退出”选择
 * 按固定顺序做深度优先尝试，失败时回退到最近的选择点尝试下一分支。
 * 每一次递归进入节点都计入步数；达到上限立即抛出并转为 capped 结果，
 * 以此把指数回溯在工程上变成可观察、可截断的现象。
 */

export interface TraceEvent {
  readonly kind: 'enter' | 'backtrack' | 'anchor' | 'match' | 'fail';
  readonly position: number;
  readonly nodeType?: RegexNode['type'];
  readonly message: string;
}

export interface RunOptions {
  readonly stepCap?: number;
  /** 收集教学用回溯轨迹；为防止轨迹本身爆炸，最多保留该数量的事件。 */
  readonly maxTraceEvents?: number;
}

export interface RunResult {
  readonly matched: boolean;
  readonly steps: number;
  readonly capped: boolean;
  readonly trace: readonly TraceEvent[];
}

const DEFAULT_STEP_CAP = 1_000_000;
const DEFAULT_MAX_TRACE = 200;

/** 延续：带着子节点消费后到达的新输入位置继续匹配剩余部分。 */
type Continuation = (nextPosition: number) => boolean;

class BacktrackingMatcher {
  private steps = 0;
  private readonly trace: TraceEvent[] = [];
  private readonly parsed: ParsedPattern;
  private readonly input: string;
  private readonly cap: number;
  private readonly maxTraceEvents: number;

  constructor(
    parsed: ParsedPattern,
    input: string,
    cap: number,
    maxTraceEvents: number,
  ) {
    this.parsed = parsed;
    this.input = input;
    this.cap = cap;
    this.maxTraceEvents = maxTraceEvents;
  }

  run(): RunResult {
    let capped = false;
    let matched = false;

    try {
      matched = this.matchWhole();
    } catch (error) {
      if (error instanceof StepCapExceededError) {
        capped = true;
      } else {
        throw error;
      }
    }

    return { matched, steps: this.steps, capped, trace: this.trace };
  }

  private record(event: TraceEvent): void {
    if (this.trace.length < this.maxTraceEvents) {
      this.trace.push(event);
    }
  }

  private tick(position: number, nodeType: RegexNode['type'], label: string): void {
    this.steps += 1;
    if (this.steps > this.cap) {
      throw new StepCapExceededError(this.cap);
    }
    if (this.trace.length < this.maxTraceEvents) {
      this.record({ kind: 'enter', position, nodeType, message: label });
    }
  }

  private matchWhole(): boolean {
    if (this.parsed.anchoredStart) {
      this.record({
        kind: 'anchor',
        position: 0,
        message: '^ 锚定：匹配必须从输入起点开始',
      });
    }

    // 整串语义：AST 匹配成功后必须恰好耗尽输入。无 $ 锚点时同样按整串判定，
    // 这样失败后缀里的死字符才能迫使所有分支回溯。
    const success = this.visit(
      this.parsed.ast,
      0,
      (nextPosition) => nextPosition === this.input.length,
    );

    if (success && this.parsed.anchoredEnd) {
      this.record({
        kind: 'anchor',
        position: this.input.length,
        message: '$ 锚定：匹配必须抵达输入终点',
      });
    }

    if (success) {
      this.record({ kind: 'match', position: this.input.length, message: '匹配成功' });
    } else {
      this.record({ kind: 'fail', position: 0, message: '所有回溯分支均失败' });
    }
    return success;
  }

  /**
   * 尝试在 position 处匹配 node；成功后用 continuation 带着新位置继续。
   * 延续式写法让“回退到选择点尝试下一条路”成为显式控制流。
   */
  private visit(node: RegexNode, position: number, continuation: Continuation): boolean {
    this.tick(position, node.type, `进入 ${node.type} @${position}`);

    switch (node.type) {
      case 'literal':
        if (this.input[position] === node.char) {
          return continuation(position + 1);
        }
        this.record({
          kind: 'backtrack',
          position,
          nodeType: 'literal',
          message: `字面量 '${node.char}' 不匹配，回退`,
        });
        return false;

      case 'group':
        return this.visit(node.child, position, continuation);

      case 'concat': {
        const matchPart = (partIndex: number, atPosition: number): boolean => {
          if (partIndex >= node.parts.length) {
            return continuation(atPosition);
          }
          return this.visit(node.parts[partIndex], atPosition, (nextPosition) =>
            matchPart(partIndex + 1, nextPosition),
          );
        };
        return matchPart(0, position);
      }

      case 'alt': {
        for (const option of node.options) {
          if (this.visit(option, position, continuation)) {
            return true;
          }
          this.record({
            kind: 'backtrack',
            position,
            nodeType: 'alt',
            message: '并集分支失败，尝试下一分支',
          });
        }
        return false;
      }

      case 'star':
        return this.matchStar(node, position, continuation);
    }
  }

  /**
   * 星号的经典回溯语义（贪婪优先）：
   *   star(child, p, k) = visit(child, p, p' -> star(child, p', k))，失败再 k(p)
   * 先走“再重复一次”的路，走不通才回退出星号；零宽子体直接退出，避免无限递归。
   */
  private matchStar(
    node: Extract<RegexNode, { type: 'star' }>,
    position: number,
    continuation: Continuation,
  ): boolean {
    const repeated = this.visit(node.child, position, (nextPosition) => {
      if (nextPosition === position) {
        return this.exitStar(position, continuation);
      }
      return this.matchStar(node, nextPosition, continuation);
    });

    if (repeated) {
      return true;
    }
    return this.exitStar(position, continuation);
  }

  private exitStar(position: number, continuation: Continuation): boolean {
    this.record({
      kind: 'backtrack',
      position,
      nodeType: 'star',
      message: '星号退出重复，交还延续',
    });
    return continuation(position);
  }
}

export function runBacktracking(
  pattern: string | RegExp | ParsedPattern,
  input: string,
  options: RunOptions = {},
): RunResult {
  const parsed =
    typeof pattern === 'string' || pattern instanceof RegExp
      ? parsePattern(pattern)
      : pattern;

  return new BacktrackingMatcher(
    parsed,
    input,
    options.stepCap ?? DEFAULT_STEP_CAP,
    options.maxTraceEvents ?? DEFAULT_MAX_TRACE,
  ).run();
}
