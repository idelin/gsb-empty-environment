/**
 * RefreshGroup：多个数据源一起刷新到同一个逻辑版本，再原子发布。
 *
 * 关键约定：
 *  - 读者通过 getView() 永远只能看到最近一次整组成功发布的一致快照，
 *    看不到任何一轮刷新过程中的中间态；
 *  - 部分失败时整组不发布，视图保持旧版本，可通过 retrySource 单独重试失败源；
 *  - 一轮只有在所有源全部成功时才发布，且 RoundPublished 事件只会产生一次；
 *  - 新一轮刷新开始后，旧轮次标记为 superseded：旧轮迟到的成功/失败结果一律丢弃，
 *    绝不能混进新一轮（也不能触发旧轮发布）；
 *  - 协调器自身不持有可变业务状态，所有状态来自事件日志 + reducer，可随时回放重建。
 */
import {
  initialState,
  reduce,
  roundOutcome,
  type GroupEvent,
  type GroupState,
  type PublishedView,
  type RoundOutcome,
  type SourceFetcher,
} from "./events.ts";
import type { EventStore } from "./store.ts";

export type RefreshResult<T> = {
  published: boolean;
  version: number | undefined; // 成功发布时的逻辑版本
  round: number;
  outcome: RoundOutcome;
  failedSources: string[];
  view: PublishedView<T>;
};

type Waiter<T> = (result: RefreshResult<T>) => void;

export class RefreshGroup<T> {
  private state: GroupState<T>;
  /** round -> 仍在进行中的取数尝试，用于在开新一轮时 abort 旧尝试。 */
  private readonly inflight = new Map<number, Map<string, AbortController>>();
  /** round -> 等待本轮结束（发布或部分失败）的回调。 */
  private readonly waiters = new Map<number, Waiter<T>[]>();

  private readonly fetchers: Readonly<Record<string, SourceFetcher<T>>>;
  private readonly store: EventStore<T>;

  constructor(
    fetchers: Readonly<Record<string, SourceFetcher<T>>>,
    store: EventStore<T>,
  ) {
    this.fetchers = fetchers;
    this.store = store;
    if (Object.keys(fetchers).length === 0) throw new Error("至少需要配置一个数据源");
    this.state = initialState<T>();
    for (const event of store.load()) this.state = reduce(this.state, event);
  }

  /** 开始一轮"全部数据源刷新到 version"。若已有未完成的轮次，旧轮整体过期。 */
  refreshAll(version: number): Promise<RefreshResult<T>> {
    const sources = Object.keys(this.fetchers);
    const round = this.state.roundSeq + 1;
    if (this.state.currentRound !== undefined) {
      const old = this.state.rounds.get(this.state.currentRound);
      if (old && !old.published && !old.superseded) {
        this.append({ type: "RoundSuperseded", round: old.round, byRound: round });
        this.abortRound(old.round);
        this.resolve(old.round, "superseded");
      }
    }
    this.append({ type: "RefreshStarted", round, version, sources });
    for (const name of sources) this.startAttempt(round, name);
    return this.waitFor(round);
  }

  /**
   * 单独重试当前轮的一个失败数据源。
   * 已成功的源、已发布/已过期的轮次都会拒绝；同一源并发的第二个重试也会拒绝，
   * 因此重试与首轮结果共用同一条到达"全部成功"的路径，发布最多一次。
   */
  retrySource(round: number, source: string): Promise<RefreshResult<T>> {
    const r = this.state.rounds.get(round);
    if (!r) throw new Error(`轮次 ${round} 不存在`);
    if (r.published) throw new Error(`轮次 ${round} 已发布，无需重试`);
    if (r.superseded) throw new Error(`轮次 ${round} 已过期，请开启新一轮刷新`);
    if (this.state.currentRound !== round) throw new Error(`轮次 ${round} 不是当前轮次`);
    const src = r.sources.get(source);
    if (!src) throw new Error(`数据源 ${source} 不在轮次 ${round} 中`);
    if (src.status === "success") throw new Error(`数据源 ${source} 已成功，不能重复刷新`);
    if (src.retryable === false) throw new Error(`数据源 ${source} 的失败不可重试`);
    // 并发去重由事件状态（SourceState.inFlight）在 reducer 中强制：
    // 若已有一次取数在途，append SourceAttemptStarted 会直接抛错。
    this.startAttempt(round, source);
    return this.waitFor(round);
  }

  /** 读者接口：一致快照。返回副本，调用方改不到内部状态。 */
  getView(): PublishedView<T> {
    return { version: this.state.view.version, sources: new Map(this.state.view.sources) };
  }

  getRound(round: number): RefreshResult<T> {
    const r = this.state.rounds.get(round);
    if (!r) throw new Error(`轮次 ${round} 不存在`);
    return this.buildResult(round, roundOutcome(r));
  }

  get currentRoundNumber(): number | undefined {
    return this.state.currentRound;
  }

  /** 调试/审计用：日志中至今的全部事件。 */
  events(): readonly GroupEvent<T>[] {
    return this.store.load();
  }

  // -- 内部 ---------------------------------------------------------------

  private append(event: GroupEvent<T>): void {
    // 先用纯 reducer 校验并算出下一状态，通过后才写入日志：
    // 非法事件（并发重试、重复发布等）不会污染事件存储。
    const next = reduce(this.state, event);
    this.store.append(event);
    this.state = next;
  }

  private startAttempt(round: number, source: string): void {
    const r = this.state.rounds.get(round)!;
    const attempt = r.sources.get(source)!.attempt + 1;
    this.append({ type: "SourceAttemptStarted", round, source, attempt });

    const controller = new AbortController();
    let bySource = this.inflight.get(round);
    if (!bySource) {
      bySource = new Map();
      this.inflight.set(round, bySource);
    }
    bySource.set(source, controller);

    const version = r.version;
    const fetcher = this.fetchers[source];
    Promise.resolve()
      .then(() => fetcher(controller.signal))
      .then(
        (res) => this.onSettled(round, source, { ok: true as const, res }),
        (error: unknown) =>
          this.onSettled(round, source, {
            ok: false as const,
            reason: error instanceof Error ? error.message : String(error),
          }),
      );
  }

  private onSettled(
    round: number,
    source: string,
    settled:
      | { ok: true; res: { value: T; version: number } }
      | { ok: false; reason: string },
  ): void {
    // 过期防护：只处理"当前轮次"的迟到结果。
    // 旧轮在被 supersede 时其 fetch 已收到 abort；即便底层忽略 abort，
    // 结果走到这里也会被直接丢弃，不可能污染新一轮或旧视图。
    if (this.state.currentRound !== round) return;
    const r = this.state.rounds.get(round);
    if (!r || r.published || r.superseded) return;
    this.inflight.get(round)?.delete(source);

    if (settled.ok) {
      const { value, version } = settled.res;
      if (version !== r.version) {
        // 源报告了与本轮逻辑版本不一致的数据，按失败处理、允许重试，
        // 绝不允许"A 新版本 + B 旧版本"冒充整组已刷新。
        this.append({
          type: "SourceFailed",
          round,
          source,
          reason: `版本不一致：期望 ${r.version}，实际 ${version}`,
          retryable: true,
        });
      } else {
        this.append({ type: "SourceSucceeded", round, source, version, value });
      }
    } else {
      this.append({
        type: "SourceFailed",
        round,
        source,
        reason: settled.reason,
        retryable: true,
      });
    }

    this.afterChange(round);
  }

  private afterChange(round: number): void {
    if (this.state.currentRound !== round) return;
    const r = this.state.rounds.get(round)!;
    const anyInflight = (this.inflight.get(round)?.size ?? 0) > 0;
    if (anyInflight) return;

    let allSuccess = true;
    const values: Array<[string, T]> = [];
    for (const [name, s] of r.sources) {
      if (s.status === "success" && s.value !== undefined && s.version === r.version) {
        values.push([name, s.value]);
      } else {
        allSuccess = false;
      }
    }

    if (allSuccess) {
      // 唯一的发布点；reducer 对同一轮重复发布会抛错。
      this.append({ type: "RoundPublished", round, version: r.version, values });
      this.resolve(round, "published");
    } else {
      // 部分失败：视图原样不动，报告失败源，等待 retrySource。
      this.resolve(round, "partial");
    }
  }

  private abortRound(round: number): void {
    const bySource = this.inflight.get(round);
    if (bySource) {
      for (const controller of bySource.values()) controller.abort();
      this.inflight.delete(round);
    }
  }

  private waitFor(round: number): Promise<RefreshResult<T>> {
    const r = this.state.rounds.get(round)!;
    const outcome = roundOutcome(r);
    if (outcome === "published" || outcome === "partial") {
      return Promise.resolve(this.buildResult(round, outcome));
    }
    return new Promise<RefreshResult<T>>((resolve) => {
      const list = this.waiters.get(round) ?? [];
      list.push(resolve);
      this.waiters.set(round, list);
    });
  }

  private resolve(round: number, outcome: RoundOutcome): void {
    const list = this.waiters.get(round);
    if (!list) return;
    this.waiters.delete(round);
    const result = this.buildResult(round, outcome);
    for (const waiter of list) waiter(result);
  }

  private buildResult(round: number, outcome: RoundOutcome): RefreshResult<T> {
    const r = this.state.rounds.get(round)!;
    const failedSources: string[] = [];
    if (outcome !== "superseded") {
      for (const [name, sourceState] of r.sources) {
        if (sourceState.status === "failed") failedSources.push(name);
      }
    }
    return {
      published: outcome === "published",
      version: outcome === "published" ? r.version : undefined,
      round,
      outcome,
      failedSources,
      view: this.getView(),
    };
  }
}
