import { stableHash } from "./hash.ts";
import type {
  AppearanceInterval,
  BusinessKey,
  CompletionEvidence,
  IncompleteEvidence,
  IncompleteReason,
  IngestResult,
  RecordChange,
  RecordState,
  RecordVersion,
  ScrollSnapshot,
  VisibleRow,
} from "./types.ts";

interface MutableAppearance extends AppearanceInterval {
  fromRound: number;
  toRound: number | null;
}

interface MutableRecord<K extends BusinessKey> {
  key: K;
  firstSeenNodeId: string;
  lastNodeId: string;
  firstSeenRound: number;
  lastSeenRound: number;
  contentHash: string;
  renderGeneration: number;
  content: unknown;
  versions: RecordVersion[];
  appearances: MutableAppearance[];
  visible: boolean;
}

interface BoundarySnapshot<K extends BusinessKey> {
  round: number;
  firstKey: K | null;
  lastKey: K | null;
  endMarked: boolean;
}

/**
 * 增量抽取器。
 *
 * 身份规则：
 * - 记录身份 = 可信业务键；nodeId 只用于检测节点换键，不参与记录匹配。
 * - 同键且内容指纹变化 => revised；同键同内容 => 无修订（仅刷新可见状态）。
 * - 节点换绑新键 => 对新键走 inserted/重新出现，旧键保留并转为不可见。
 * - 某键在本轮快照缺失 => 不可见，绝不删除。
 *
 * 完成规则：显式结束标记连续出现，且首/末边界键连续两轮不变，才 complete。
 * 顺序回绕接缝或边界历史回访 => 输出未完成证据（循环滚动永不完成）。
 */
export class IncrementalExtractor<K extends BusinessKey = BusinessKey> {
  readonly #records = new Map<K, MutableRecord<K>>();
  readonly #nodeToKey = new Map<string, K>();
  readonly #discoverySeq = new Map<K, number>();
  readonly #boundaryHistory: BoundarySnapshot<K>[] = [];
  #discoveryCounter = 0;
  #endMarkedStreak = 0;
  #boundaryStableStreak = 0;
  #lastBoundary: { firstKey: K | null; lastKey: K | null } | null = null;
  #completionRound: number | null = null;

  /** 已采集记录数（只增不减，缺失行不删除）。 */
  get size(): number {
    return this.#records.size;
  }

  get completed(): boolean {
    return this.#completionRound !== null;
  }

  get completionRound(): number | null {
    return this.#completionRound;
  }

  getRecord(key: K): RecordState<K> | undefined {
    const record = this.#records.get(key);
    return record ? this.#freeze(record) : undefined;
  }

  getRecords(): RecordState<K>[] {
    return [...this.#records.values()].map((record) => this.#freeze(record));
  }

  /** 按业务键发现顺序返回全部键。 */
  keys(): K[] {
    return [...this.#discoverySeq.entries()]
      .sort((left, right) => left[1] - right[1])
      .map(([key]) => key);
  }

  ingest(snapshot: ScrollSnapshot<K>): IngestResult<K> {
    const round = snapshot.round;
    const changes: RecordChange<K>[] = [];
    const visibleKeys: K[] = [];
    const nodeRebinds: IngestResult<K>["nodeRebinds"] = [];
    // 在任何记录写入之前确定本轮首次出现的键，供回绕接缝检测排除头部插入。
    const newKeys = new Set<K>();
    for (const row of snapshot.rows) {
      if (!this.#records.has(row.key)) newKeys.add(row.key);
    }

    // 先把上一轮可见记录标记为不可见候选，本轮出现的会重新打开。
    for (const record of this.#records.values()) record.visible = false;

    // 本轮 nodeId -> key 映射，单独构建以便检测同一节点换键。
    const currentNodeKeys = new Map<string, K>();
    const currentKeys = new Set<K>();
    for (const row of snapshot.rows) {
      const boundKey = currentNodeKeys.get(row.nodeId);
      if (boundKey !== undefined && boundKey !== row.key) {
        throw new Error(
          `快照内节点 ${row.nodeId} 在同一轮绑定了多个业务键，数据不合法`,
        );
      }
      currentNodeKeys.set(row.nodeId, row.key);
      if (currentKeys.has(row.key)) {
        throw new Error(`快照内业务键 ${String(row.key)} 在同一轮重复出现，数据不合法`);
      }
      currentKeys.add(row.key);
    }

    for (const row of snapshot.rows) {
      this.#handleNodeRebind(row, nodeRebinds);
      this.#handleRow(row, round, changes);
      visibleKeys.push(row.key);
    }

    // 本轮未出现的记录：仅关闭出现区间，不删除。
    const hiddenKeys: K[] = [];
    for (const record of this.#records.values()) {
      if (!record.visible) {
        const openInterval = record.appearances[record.appearances.length - 1];
        if (openInterval && openInterval.toRound === null) {
          openInterval.toRound = round;
          hiddenKeys.push(record.key);
        }
      }
    }

    const firstKey = snapshot.rows.length ? snapshot.rows[0].key : null;
    const lastKey = snapshot.rows.length
      ? snapshot.rows[snapshot.rows.length - 1].key
      : null;

    const wrapSeam = this.#detectWrapSeam(snapshot.rows, newKeys);
    const { completionEvidence, incompleteEvidence } = this.#evaluateCompletion(
      round,
      firstKey,
      lastKey,
      snapshot.endMarked,
      wrapSeam,
    );

    this.#boundaryHistory.push({ round, firstKey, lastKey, endMarked: snapshot.endMarked });

    return {
      round,
      changes,
      visibleKeys,
      hiddenKeys,
      nodeRebinds,
      complete: this.#completionRound !== null,
      completionEvidence,
      incompleteEvidence,
    };
  }

  #handleNodeRebind(
    row: VisibleRow<K>,
    nodeRebinds: IngestResult<K>["nodeRebinds"],
  ): void {
    const previousKey = this.#nodeToKey.get(row.nodeId);
    if (previousKey !== undefined && previousKey !== row.key) {
      nodeRebinds.push({ nodeId: row.nodeId, fromKey: previousKey, toKey: row.key });
      // 节点换键：旧记录保留（本轮缺失即不可见），节点身份改挂新键。
      this.#nodeToKey.set(row.nodeId, row.key);
    }
  }

  #handleRow(row: VisibleRow<K>, round: number, changes: RecordChange<K>[]): void {
    const hash = stableHash(row.content);
    let record = this.#records.get(row.key);

    if (!record) {
      record = {
        key: row.key,
        firstSeenNodeId: row.nodeId,
        lastNodeId: row.nodeId,
        firstSeenRound: round,
        lastSeenRound: round,
        contentHash: hash,
        renderGeneration: row.renderGeneration,
        content: row.content,
        versions: [
          {
            round,
            renderGeneration: row.renderGeneration,
            contentHash: hash,
            content: row.content,
          },
        ],
        appearances: [{ fromRound: round, toRound: null }],
        visible: true,
      };
      this.#records.set(row.key, record);
      this.#discoverySeq.set(row.key, this.#discoveryCounter++);
      if (!this.#nodeToKey.has(row.nodeId)) this.#nodeToKey.set(row.nodeId, row.key);
      changes.push({
        key: row.key,
        kind: "inserted",
        round,
        nodeId: row.nodeId,
        previousNodeId: null,
        renderGeneration: row.renderGeneration,
        previousContentHash: null,
        contentHash: hash,
        content: row.content,
      });
      return;
    }

    const previousNodeId = record.lastNodeId;
    record.lastNodeId = row.nodeId;
    record.lastSeenRound = round;
    record.renderGeneration = row.renderGeneration;
    if (!this.#nodeToKey.has(row.nodeId)) this.#nodeToKey.set(row.nodeId, row.key);

    const wasVisible = record.visible;
    record.visible = true;
    if (!wasVisible) {
      record.appearances.push({ fromRound: round, toRound: null });
    }

    if (record.contentHash !== hash) {
      record.contentHash = hash;
      record.content = row.content;
      record.versions.push({
        round,
        renderGeneration: row.renderGeneration,
        contentHash: hash,
        content: row.content,
      });
      changes.push({
        key: row.key,
        kind: "revised",
        round,
        nodeId: row.nodeId,
        previousNodeId,
        renderGeneration: row.renderGeneration,
        previousContentHash: record.versions[record.versions.length - 2].contentHash,
        contentHash: hash,
        content: row.content,
      });
    } else if (!wasVisible) {
      // 内容未变但记录曾离开视口：输出重新出现事件，保留出现区间语义。
      changes.push({
        key: row.key,
        kind: "reseen",
        round,
        nodeId: row.nodeId,
        previousNodeId,
        renderGeneration: row.renderGeneration,
        previousContentHash: hash,
        contentHash: hash,
        content: row.content,
      });
    }
  }

  /**
   * 检测顺序回绕接缝：相邻两行中，后一个键的发现序号早于前一个键，
   * 且两个键都不是本轮首次发现（头部插入的新键会被排除）。
   */
  #detectWrapSeam(
    rows: ReadonlyArray<VisibleRow<K>>,
    newKeys: ReadonlySet<K>,
  ): { before: K; after: K } | null {
    for (let index = 1; index < rows.length; index += 1) {
      const before = rows[index - 1];
      const after = rows[index];
      if (newKeys.has(before.key) || newKeys.has(after.key)) continue;
      const beforeSeq = this.#discoverySeq.get(before.key);
      const afterSeq = this.#discoverySeq.get(after.key);
      if (beforeSeq === undefined || afterSeq === undefined) continue;
      if (afterSeq < beforeSeq) return { before: before.key, after: after.key };
    }
    return null;
  }

  #evaluateCompletion(
    round: number,
    firstKey: K | null,
    lastKey: K | null,
    endMarked: boolean,
    wrapSeam: { before: K; after: K } | null,
  ): {
    completionEvidence: CompletionEvidence<K> | null;
    incompleteEvidence: IncompleteEvidence<K> | null;
  } {
    this.#endMarkedStreak = endMarked ? this.#endMarkedStreak + 1 : 0;

    const boundarySame =
      this.#lastBoundary !== null &&
      this.#lastBoundary.firstKey === firstKey &&
      this.#lastBoundary.lastKey === lastKey;
    this.#boundaryStableStreak = boundarySame ? this.#boundaryStableStreak + 1 : 1;
    this.#lastBoundary = { firstKey, lastKey };

    let revisitedAtRound: number | null = null;
    if (this.#boundaryStableStreak === 1 && firstKey !== null && lastKey !== null) {
      const revisit = this.#boundaryHistory.find(
        (item) => item.firstKey === firstKey && item.lastKey === lastKey,
      );
      if (revisit) revisitedAtRound = revisit.round;
    }

    const reasons: IncompleteReason[] = [];
    if (wrapSeam) reasons.push("wrap-seam");
    if (this.#endMarkedStreak < 2) reasons.push("end-not-marked");
    if (this.#endMarkedStreak >= 2 && this.#boundaryStableStreak < 2) {
      reasons.push("boundary-changed");
    }
    if (revisitedAtRound !== null) reasons.push("boundary-revisit");

    const boundaryStable = this.#boundaryStableStreak >= 2;
    const endStable = this.#endMarkedStreak >= 2;

    if (boundaryStable && endStable) {
      this.#completionRound = round;
      return {
        completionEvidence: {
          round,
          firstKey,
          lastKey,
          endMarkedStreak: this.#endMarkedStreak,
          boundaryStableStreak: this.#boundaryStableStreak,
        },
        incompleteEvidence: null,
      };
    }

    return {
      completionEvidence: null,
      incompleteEvidence: {
        round,
        reasons,
        firstKey,
        lastKey,
        wrapSeam,
        revisitedAtRound,
      },
    };
  }

  #freeze(record: MutableRecord<K>): RecordState<K> {
    return {
      key: record.key,
      firstSeenNodeId: record.firstSeenNodeId,
      lastNodeId: record.lastNodeId,
      firstSeenRound: record.firstSeenRound,
      lastSeenRound: record.lastSeenRound,
      contentHash: record.contentHash,
      renderGeneration: record.renderGeneration,
      content: record.content,
      versions: record.versions.map((version) => ({ ...version })),
      appearances: record.appearances.map((interval) => ({ ...interval })),
      visible: record.visible,
    };
  }
}
