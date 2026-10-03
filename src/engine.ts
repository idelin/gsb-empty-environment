/**
 * 多模式复合事件序列匹配器（CEP 引擎）
 *
 * 核心模型：非确定性有向无环图（NFA，状态图为 DAG）。
 * 每个注册模式展开为状态链  S0 --first--> S1 --second--> S2(final)
 * 在 S1 上挂否定边（negation）：窗口内出现否定事件则该运行实例进入死态。
 * 每条 first 事件产生一个活跃运行实例（run），因此同一模式可并发存在
 * 多条部分匹配 —— 即 NFA 的非确定性。
 *
 * 能力：
 *  - 跨事件时间滑窗：B 必须落在 (A.t, A.t + windowMs] 内；
 *  - 乱序补偿：事件可乱序到达。迟到的 A/B 会基于事件缓冲区补偿触发匹配；
 *  - 精准撤销：迟到的否定事件 C 只撤销时间区间严格包含 C 的已发命中；
 *  - 模式超时：水位线越过 A.t + windowMs 后，未完成的运行被丢弃并记日志。
 */

export interface CepEvent {
  id: string;
  type: string;
  /** 事件时间，毫秒 */
  timestamp: number;
}

export interface PatternSpec {
  /** 模式名（唯一） */
  name: string;
  /** 序列模式，如 ['A', 'B'] 表示 A -> B */
  sequence: [string, string];
  /** 否定事件类型：A 与 B 之间不得出现，如 ['C'] */
  negation?: string[];
  /** 滑动窗口（毫秒）：B 必须在 A 之后 windowMs 内出现 */
  windowMs: number;
}

export type Notification =
  | {
      kind: 'match';
      pattern: string;
      matchId: string;
      events: [CepEvent, CepEvent];
      /** true 表示由乱序事件补偿触发 */
      compensated: boolean;
    }
  | {
      kind: 'retract';
      pattern: string;
      matchId: string;
      reason: string;
      cause: CepEvent;
    };

export type LogAction =
  | 'run-started'
  | 'run-killed-by-negation'
  | 'run-timeout'
  | 'match'
  | 'match-compensated'
  | 'retract';

export interface LogEntry {
  /** 记录时的水位线（已见最大事件时间） */
  watermark: number;
  action: LogAction;
  pattern: string;
  detail: string;
}

interface Run {
  id: number;
  anchor: CepEvent; // 命中的 first（A）事件
}

interface MatchRecord {
  id: string;
  a: CepEvent;
  b: CepEvent;
  retracted: boolean;
}

interface PatternState {
  spec: PatternSpec;
  runs: Run[];
  matches: Map<string, MatchRecord>;
}

export class CepEngine {
  private patterns = new Map<string, PatternState>();
  /** 已到达事件的缓冲区，按事件时间升序，用于乱序补偿 */
  private buffer: CepEvent[] = [];
  /** 水位线：已见事件的最大事件时间 */
  private watermark = Number.NEGATIVE_INFINITY;
  private listeners: Array<(n: Notification) => void> = [];
  private runSeq = 0;
  private matchSeq = 0;

  readonly notifications: Notification[] = [];
  readonly log: LogEntry[] = [];

  registerPattern(spec: PatternSpec): void {
    if (this.patterns.has(spec.name)) {
      throw new Error(`pattern already registered: ${spec.name}`);
    }
    this.patterns.set(spec.name, { spec, runs: [], matches: new Map() });
  }

  onNotification(listener: (n: Notification) => void): void {
    this.listeners.push(listener);
  }

  /** 投喂一个事件；允许事件时间乱序。 */
  process(event: CepEvent): void {
    const late = event.timestamp < this.watermark;
    if (event.timestamp > this.watermark) this.watermark = event.timestamp;

    const pos = this.lowerBound(this.buffer, event.timestamp);
    this.buffer.splice(pos, 0, event);

    for (const state of this.patterns.values()) {
      this.applyEvent(state, event, late);
    }
    this.expireRuns();
    this.pruneBuffer();
  }

  private applyEvent(state: PatternState, event: CepEvent, late: boolean): void {
    const { spec } = state;
    const [first, second] = spec.sequence;
    const negated = spec.negation ?? [];

    if (event.type === first) {
      const run: Run = { id: ++this.runSeq, anchor: event };
      state.runs.push(run);
      this.logLine(state, 'run-started', `run#${run.id} anchored at ${event.type}@${event.timestamp}`);
      if (late) {
        // 乱序补偿：用缓冲区中“已经发生”的后续事件立即结算该运行
        this.resolveLateRun(state, run);
      }
      return;
    }

    if (event.type === second) {
      for (const run of [...state.runs]) {
        const dt = event.timestamp - run.anchor.timestamp;
        if (dt > 0 && dt <= spec.windowMs) {
          this.removeRun(state, run);
          this.emitMatch(state, run.anchor, event, late);
        }
      }
      return;
    }

    if (negated.includes(event.type)) {
      // 1) 杀死窗口内尚未完成的部分匹配
      for (const run of [...state.runs]) {
        const dt = event.timestamp - run.anchor.timestamp;
        if (dt > 0 && dt <= spec.windowMs) {
          this.removeRun(state, run);
          this.logLine(
            state,
            'run-killed-by-negation',
            `run#${run.id} killed by ${event.type}@${event.timestamp}`,
          );
        }
      }
      // 2) 精准撤销：仅撤销时间区间 (A.t, B.t) 严格包含该否定事件的已发命中
      for (const match of state.matches.values()) {
        if (
          !match.retracted &&
          event.timestamp > match.a.timestamp &&
          event.timestamp < match.b.timestamp
        ) {
          match.retracted = true;
          const notification: Notification = {
            kind: 'retract',
            pattern: spec.name,
            matchId: match.id,
            reason: `negation ${event.type}@${event.timestamp} falls inside (${match.a.timestamp}, ${match.b.timestamp})`,
            cause: event,
          };
          this.dispatch(notification);
          this.logLine(
            state,
            'retract',
            `${match.id} retracted: negation ${event.type}#${event.id}@${event.timestamp} inside (${match.a.timestamp}, ${match.b.timestamp})`,
          );
        }
      }
    }
  }

  /** 迟到的 A：扫描缓冲区，按事件时间序找到第一个否定事件或 B，先者生效。 */
  private resolveLateRun(state: PatternState, run: Run): void {
    const { spec } = state;
    const from = run.anchor.timestamp;
    const until = from + spec.windowMs;
    const negated = new Set(spec.negation ?? []);
    const second = spec.sequence[1];

    for (const e of this.buffer) {
      if (e.timestamp <= from) continue;
      if (e.timestamp > until) break;
      if (negated.has(e.type)) {
        this.removeRun(state, run);
        this.logLine(
          state,
          'run-killed-by-negation',
          `run#${run.id} killed by late-seen ${e.type}@${e.timestamp}`,
        );
        return;
      }
      if (e.type === second) {
        this.removeRun(state, run);
        this.emitMatch(state, run.anchor, e, true);
        return;
      }
    }
  }

  private emitMatch(state: PatternState, a: CepEvent, b: CepEvent, compensated: boolean): void {
    const matchId = `m-${++this.matchSeq}`;
    state.matches.set(matchId, { id: matchId, a, b, retracted: false });
    const notification: Notification = {
      kind: 'match',
      pattern: state.spec.name,
      matchId,
      events: [a, b],
      compensated,
    };
    this.dispatch(notification);
    this.logLine(
      state,
      compensated ? 'match-compensated' : 'match',
      `${matchId}: ${a.type}#${a.id}@${a.timestamp} -> ${b.type}#${b.id}@${b.timestamp}` +
        (compensated ? ' (out-of-order compensation)' : ''),
    );
  }

  /** 模式超时丢弃：水位线越过 anchor + window 的未完成运行被丢弃。 */
  private expireRuns(): void {
    for (const state of this.patterns.values()) {
      for (const run of [...state.runs]) {
        if (run.anchor.timestamp + state.spec.windowMs < this.watermark) {
          this.removeRun(state, run);
          this.logLine(
            state,
            'run-timeout',
            `run#${run.id} discarded: deadline ${run.anchor.timestamp + state.spec.windowMs} < watermark ${this.watermark}`,
          );
        }
      }
    }
  }

  /** 缓冲区只保留水位线回退最大窗口内的事件，更早的事件不可能再参与新匹配。 */
  private pruneBuffer(): void {
    let maxWindow = 0;
    for (const state of this.patterns.values()) {
      maxWindow = Math.max(maxWindow, state.spec.windowMs);
    }
    const horizon = this.watermark - maxWindow;
    let drop = 0;
    while (drop < this.buffer.length && this.buffer[drop].timestamp < horizon) drop++;
    if (drop > 0) this.buffer.splice(0, drop);
  }

  private removeRun(state: PatternState, run: Run): void {
    const i = state.runs.indexOf(run);
    if (i >= 0) state.runs.splice(i, 1);
  }

  private dispatch(n: Notification): void {
    this.notifications.push(n);
    for (const fn of this.listeners) fn(n);
  }

  private logLine(state: PatternState, action: LogAction, detail: string): void {
    this.log.push({ watermark: this.watermark, action, pattern: state.spec.name, detail });
  }

  private lowerBound(arr: CepEvent[], ts: number): number {
    let lo = 0;
    let hi = arr.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (arr[mid].timestamp < ts) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }
}
