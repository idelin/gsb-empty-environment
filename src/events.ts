/**
 * 事件日志（Event Sourcing）定义。
 *
 * 所有状态变更都只能通过 append 一个事件来完成；reducer 是纯函数，
 * 给定同一段事件序列必然得到同一个状态 —— 这就是"状态操作可回放"。
 *
 * 异步副作用（fetch、AbortController、Promise 回调）全部留在 Coordinator 里，
 * 事件里不保存任何无法序列化的对象，因此日志可以持久化、传输、重建状态。
 */

export type SourceResult<T> = { value: T; version: number };

/** 单个数据源的取数函数；失败时抛出或返回 rejected Promise。 */
export type SourceFetcher<T> = (signal: AbortSignal) => Promise<SourceResult<T>>;

export type FetchStatus = "success" | "failed";

/** 一轮刷新的对外状态。 */
export type RoundOutcome =
  | "idle"
  | "running"
  | "partial" // 有失败源：整组不发布，保持旧视图
  | "published" // 全部成功：整组原子发布一次
  | "superseded"; // 被更新的一轮取代，迟到结果一律丢弃

export type PublishedView<T> = {
  version: number; // 逻辑版本；0 表示尚无任何已发布版本
  sources: ReadonlyMap<string, T>;
};

// ---------------------------------------------------------------------------
// 事件
// ---------------------------------------------------------------------------

export type GroupEvent<T> =
  | { type: "RefreshStarted"; round: number; version: number; sources: string[] }
  | { type: "SourceAttemptStarted"; round: number; source: string; attempt: number }
  | {
      type: "SourceSucceeded";
      round: number;
      source: string;
      version: number;
      value: T;
    }
  | {
      type: "SourceFailed";
      round: number;
      source: string;
      reason: string;
      retryable: boolean;
    }
  | {
      type: "RoundPublished";
      round: number;
      version: number;
      values: ReadonlyArray<readonly [string, T]>;
    }
  | { type: "RoundSuperseded"; round: number; byRound: number };

export type SourceState<T> = {
  status: FetchStatus;
  attempt: number;
  inFlight: boolean; // 当前是否有一次取数在途（并发去重同样由事件状态强制）
  value: T | undefined;
  version: number | undefined;
  reason: string | undefined;
  retryable: boolean;
};

export type RoundState<T> = {
  round: number;
  version: number;
  sources: ReadonlyMap<string, SourceState<T>>;
  published: boolean;
  superseded: boolean;
};

export type GroupState<T> = {
  /** 最近一次"整组原子发布"的一致视图，读者只能读到它。 */
  view: PublishedView<T>;
  roundSeq: number;
  currentRound: number | undefined;
  rounds: ReadonlyMap<number, RoundState<T>>;
};

export function initialState<T>(): GroupState<T> {
  return {
    view: { version: 0, sources: new Map() },
    roundSeq: 0,
    currentRound: undefined,
    rounds: new Map(),
  };
}

const err = (m: string): Error => new Error(m);

/** 纯函数 reducer：校验事件与当前状态是否匹配并推进状态。 */
export function reduce<T>(state: GroupState<T>, event: GroupEvent<T>): GroupState<T> {
  switch (event.type) {
    case "RefreshStarted": {
      if (event.round !== state.roundSeq + 1) {
        throw err(`非法轮次序号 RefreshStarted.round=${event.round}，期望 ${state.roundSeq + 1}`);
      }
      if (event.sources.length === 0) throw err("RefreshStarted 至少需要一个数据源");
      if (new Set(event.sources).size !== event.sources.length) {
        throw err("数据源名称存在重复");
      }
      if (!Number.isInteger(event.version) || event.version <= 0) {
        throw err("逻辑版本必须是正整数");
      }
      const sources = new Map<string, SourceState<T>>();
      for (const name of event.sources) {
        sources.set(name, {
          status: "failed",
          attempt: 0,
          inFlight: false,
          value: undefined,
          version: undefined,
          reason: undefined,
          retryable: true,
        });
      }
      const rounds = new Map(state.rounds).set(event.round, {
        round: event.round,
        version: event.version,
        sources,
        published: false,
        superseded: false,
      });
      return { ...state, roundSeq: event.round, currentRound: event.round, rounds };
    }

    case "SourceAttemptStarted": {
      const r = state.rounds.get(event.round);
      if (!r) throw err(`轮次 ${event.round} 不存在`);
      if (r.published) throw err(`轮次 ${event.round} 已发布，不能再取数`);
      if (r.superseded) throw err(`轮次 ${event.round} 已过期`);
      const prev = r.sources.get(event.source);
      if (!prev) throw err(`轮次 ${event.round} 中没有数据源 ${event.source}`);
      if (event.attempt !== prev.attempt + 1) {
        throw err(
          `attempt 序号不连续：${event.source} 收到 ${event.attempt}，期望 ${prev.attempt + 1}`,
        );
      }
      if (prev.inFlight) throw err(`数据源 ${event.source} 已有一次取数在途，禁止并发重试`);
      if (prev.status === "success") throw err(`数据源 ${event.source} 已成功，不能重复取数`);
      const started: SourceState<T> = { ...prev, attempt: event.attempt, inFlight: true };
      return withSource(state, r, event.source, started);
    }

    case "SourceSucceeded": {
      const r = state.rounds.get(event.round);
      if (!r) throw err(`轮次 ${event.round} 不存在`);
      if (r.published) throw err(`轮次 ${event.round} 已发布`);
      if (r.superseded) throw err(`轮次 ${event.round} 已过期`);
      const prev = r.sources.get(event.source);
      if (!prev) throw err(`轮次 ${event.round} 中没有数据源 ${event.source}`);
      if (prev.attempt < 1 || !prev.inFlight) {
        throw err(`${event.source} 没有在途的取数尝试，不能落地成功结果`);
      }
      const next: SourceState<T> = {
        status: "success",
        attempt: prev.attempt,
        inFlight: false,
        value: event.value,
        version: event.version,
        reason: undefined,
        retryable: false,
      };
      return withSource(state, r, event.source, next);
    }

    case "SourceFailed": {
      const r = state.rounds.get(event.round);
      if (!r) throw err(`轮次 ${event.round} 不存在`);
      if (r.published) throw err(`轮次 ${event.round} 已发布`);
      if (r.superseded) throw err(`轮次 ${event.round} 已过期`);
      const prev = r.sources.get(event.source);
      if (!prev) throw err(`轮次 ${event.round} 中没有数据源 ${event.source}`);
      if (prev.attempt < 1 || !prev.inFlight) {
        throw err(`${event.source} 没有在途的取数尝试，不能落地失败结果`);
      }
      const next: SourceState<T> = {
        status: "failed",
        attempt: prev.attempt,
        inFlight: false,
        value: undefined,
        version: undefined,
        reason: event.reason,
        retryable: event.retryable,
      };
      return withSource(state, r, event.source, next);
    }

    case "RoundPublished": {
      const r = state.rounds.get(event.round);
      if (!r) throw err(`轮次 ${event.round} 不存在`);
      if (r.published) throw err(`轮次 ${event.round} 已发布过（发布必须且只能一次）`);
      if (r.superseded) throw err(`过期轮次 ${event.round} 不允许发布`);
      if (r.version !== event.version) throw err("发布版本与轮次版本不一致");
      const values = new Map(event.values);
      for (const [name, s] of r.sources) {
        if (s.status !== "success" || s.value === undefined || s.version !== r.version) {
          throw err(`数据源 ${name} 未在版本 ${r.version} 上成功，不能整组发布`);
        }
        if (!values.has(name)) throw err(`发布快照缺少数据源 ${name}`);
      }
      if (values.size !== r.sources.size) throw err("发布快照包含多余的数据源");
      const newRounds = new Map(state.rounds).set(event.round, { ...r, published: true });
      return {
        ...state,
        rounds: newRounds,
        view: { version: event.version, sources: values },
      };
    }

    case "RoundSuperseded": {
      const r = state.rounds.get(event.round);
      if (!r) throw err(`轮次 ${event.round} 不存在`);
      if (r.published) throw err(`已发布轮次 ${event.round} 不会过期`);
      if (event.byRound <= event.round) throw err("新轮次必须更晚");
      const newRounds = new Map(state.rounds).set(event.round, { ...r, superseded: true });
      return {
        ...state,
        rounds: newRounds,
        currentRound: state.currentRound === event.round ? event.byRound : state.currentRound,
      };
    }
  }
}

function withSource<T>(
  state: GroupState<T>,
  round: RoundState<T>,
  source: string,
  next: SourceState<T>,
): GroupState<T> {
  const sources = new Map(round.sources).set(source, next);
  const newRounds = new Map(state.rounds).set(round.round, { ...round, sources });
  return { ...state, rounds: newRounds };
}

/** 由状态推导一轮的对外结果（派生值，不落日志）。 */
export function roundOutcome<T>(r: RoundState<T>): RoundOutcome {
  if (r.published) return "published";
  if (r.superseded) return "superseded";
  let started = false;
  let anyInFlight = false;
  let anyFailed = false;
  for (const source of r.sources.values()) {
    if (source.attempt > 0) started = true;
    if (source.inFlight) anyInFlight = true;
    if (source.status === "failed") anyFailed = true;
  }
  if (!started) return "idle";
  if (anyFailed && !anyInFlight) return "partial";
  return "running";
}
