/**
 * 多数据源原子刷新协调器。
 *
 * 设计要点：
 * - 事件溯源：所有状态变更先追加为不可变事件，状态由事件折叠（fold）得到，
 *   因此任意时刻都可以通过 replay(log) 完整回放重建。
 * - 读者隔离：读者只能拿到 getView() 返回的深冻结快照；该快照仅在
 *   Committed 事件应用时被整体替换（单指针切换），事务中间态（round 内的
 *   loaded/failed 集合）对读者不可见。
 * - 版本一致性：源上报的版本必须等于本轮目标版本，否则记为该源失败，
 *   "A 新 B 旧" 无法冒充整组已刷新。
 * - 恰好发布一次：Committed 由 committed 标志守卫，重试补齐后只提交一次。
 * - 轮次围栏：上报必须携带轮次 id，过期轮次的迟到结果会被拒绝，
 *   不会混入新一轮。
 */

export type SourceName = string;

export interface SourcePayload {
  version: number;
  data: unknown;
}

export type Event =
  | { type: 'RoundStarted'; round: number; targetVersion: number; sources: SourceName[] }
  | { type: 'SourceLoaded'; round: number; source: SourceName; version: number; data: unknown }
  | { type: 'SourceFailed'; round: number; source: SourceName; reason: string }
  | { type: 'Committed'; round: number; version: number };

export interface View {
  version: number;
  data: Readonly<Record<SourceName, unknown>>;
}

export type ReportResult =
  | { accepted: true }
  | { accepted: false; reason: string };

export interface RefreshSummary {
  round: number;
  committed: boolean;
  failedSources: SourceName[];
  view: View;
}

interface RoundState {
  id: number;
  targetVersion: number;
  loaded: Map<SourceName, SourcePayload>;
  failed: Map<SourceName, string>;
  committed: boolean;
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object' && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const key of Object.keys(value as Record<string, unknown>)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
  }
  return value;
}

const INITIAL_VIEW: View = deepFreeze({ version: 0, data: {} });

export class RefreshCoordinator {
  #sources: readonly SourceName[];
  #log: Event[] = [];
  #view: View = INITIAL_VIEW;
  #round: RoundState | null = null;
  #roundCounter = 0;

  constructor(sources: SourceName[]) {
    if (sources.length === 0) throw new Error('at least one source is required');
    this.#sources = [...sources];
  }

  /** 从事件日志回放重建（可回放性的公开入口）。 */
  static replay(sources: SourceName[], events: readonly Event[]): RefreshCoordinator {
    const coordinator = new RefreshCoordinator(sources);
    for (const event of events) coordinator.#apply(event);
    coordinator.#log.push(...events);
    coordinator.#roundCounter = events.reduce(
      (max, e) => ('round' in e ? Math.max(max, e.round) : max),
      0,
    );
    return coordinator;
  }

  /** 读者唯一入口：返回已发布的一致性快照（深冻结，事务中间态不可见）。 */
  getView(): View {
    return this.#view;
  }

  /** 完整事件日志（只读），可用于回放与审计。 */
  getLog(): readonly Event[] {
    return this.#log;
  }

  /** 当前轮的失败源及原因。 */
  getFailedSources(): ReadonlyMap<SourceName, string> {
    return this.#round ? new Map(this.#round.failed) : new Map();
  }

  get currentRound(): number {
    return this.#round?.id ?? 0;
  }

  /** 开启新一轮刷新。开启新轮即作废旧轮：旧轮迟到的结果将被围栏拒绝。 */
  startRefresh(targetVersion: number): number {
    const round = ++this.#roundCounter;
    this.#dispatch({ type: 'RoundStarted', round, targetVersion, sources: [...this.#sources] });
    return round;
  }

  /**
   * 上报某源的结果。版本不等于本轮目标版本时记为该源失败
   * （"A 新 B 旧"不会提交）；过期轮次/未知源/已提交轮次一律拒绝。
   */
  reportSource(round: number, source: SourceName, payload: SourcePayload): ReportResult {
    const state = this.#round;
    if (!state || state.id !== round) {
      return { accepted: false, reason: `stale-round: round ${round} is not active` };
    }
    if (state.committed) {
      return { accepted: false, reason: `round ${round} already committed` };
    }
    if (!this.#sources.includes(source)) {
      return { accepted: false, reason: `unknown source: ${source}` };
    }
    if (payload.version !== state.targetVersion) {
      const reason = `version-mismatch: got ${payload.version}, want ${state.targetVersion}`;
      this.#dispatch({ type: 'SourceFailed', round, source, reason });
      return { accepted: false, reason };
    }
    this.#dispatch({ type: 'SourceLoaded', round, source, version: payload.version, data: payload.data });
    this.#commitIfComplete(state);
    return { accepted: true };
  }

  /** 显式标记某源失败（如抓取抛错）。 */
  failSource(round: number, source: SourceName, reason: string): ReportResult {
    const state = this.#round;
    if (!state || state.id !== round) {
      return { accepted: false, reason: `stale-round: round ${round} is not active` };
    }
    if (state.committed) {
      return { accepted: false, reason: `round ${round} already committed` };
    }
    if (!this.#sources.includes(source)) {
      return { accepted: false, reason: `unknown source: ${source}` };
    }
    this.#dispatch({ type: 'SourceFailed', round, source, reason });
    return { accepted: true };
  }

  /** 并发抓取全部源并上报；部分失败时保持旧视图，返回失败源列表。 */
  async refreshAll(
    targetVersion: number,
    fetchers: Record<SourceName, () => Promise<SourcePayload>>,
  ): Promise<RefreshSummary> {
    const round = this.startRefresh(targetVersion);
    await Promise.all(
      this.#sources.map(async (source) => {
        try {
          const payload = await fetchers[source]();
          this.reportSource(round, source, payload);
        } catch (error) {
          this.failSource(round, source, error instanceof Error ? error.message : String(error));
        }
      }),
    );
    return this.#summary(round);
  }

  /** 单源重试：只重抓一个源；若补齐整组则提交（且只提交一次）。 */
  async retrySource(
    source: SourceName,
    fetcher: () => Promise<SourcePayload>,
  ): Promise<RefreshSummary> {
    const state = this.#round;
    if (!state) throw new Error('no active round to retry in');
    if (state.committed) throw new Error(`round ${state.id} already committed`);
    const round = state.id;
    try {
      const payload = await fetcher();
      this.reportSource(round, source, payload);
    } catch (error) {
      this.failSource(round, source, error instanceof Error ? error.message : String(error));
    }
    return this.#summary(round);
  }

  #summary(round: number): RefreshSummary {
    return {
      round,
      committed: this.#round?.id === round ? this.#round.committed : true,
      failedSources: this.#round ? [...this.#round.failed.keys()] : [],
      view: this.#view,
    };
  }

  #commitIfComplete(state: RoundState): void {
    if (state.committed) return;
    const complete = this.#sources.every((s) => state.loaded.has(s));
    if (!complete) return;
    this.#dispatch({ type: 'Committed', round: state.id, version: state.targetVersion });
  }

  #dispatch(event: Event): void {
    this.#log.push(event);
    this.#apply(event);
  }

  /** 唯一的状态转移函数：回放与实时路径共用，保证语义一致。 */
  #apply(event: Event): void {
    switch (event.type) {
      case 'RoundStarted':
        this.#round = {
          id: event.round,
          targetVersion: event.targetVersion,
          loaded: new Map(),
          failed: new Map(),
          committed: false,
        };
        break;
      case 'SourceLoaded':
        if (this.#round?.id === event.round) {
          this.#round.failed.delete(event.source);
          this.#round.loaded.set(event.source, { version: event.version, data: event.data });
        }
        break;
      case 'SourceFailed':
        if (this.#round?.id === event.round) {
          this.#round.loaded.delete(event.source);
          this.#round.failed.set(event.source, event.reason);
        }
        break;
      case 'Committed': {
        const state = this.#round;
        if (!state || state.id !== event.round || state.committed) break;
        const data: Record<SourceName, unknown> = {};
        for (const source of this.#sources) {
          data[source] = state.loaded.get(source)!.data;
        }
        // 单指针切换：读者要么看到旧快照，要么看到完整新快照。
        this.#view = deepFreeze({ version: event.version, data });
        state.committed = true;
        break;
      }
    }
  }
}
