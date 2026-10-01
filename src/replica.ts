/**
 * 副本（Replica）：分布式键值存储的单个节点。
 *
 * 客户端协议（Riak 风格）：
 *   1. get(key)  → 返回当前所有分支值（siblings）与 Context（版本向量）。
 *   2. put(key, value, context) → 客户端提交新数据时出示上次读取得到的 Context：
 *      - Context 覆盖本地现有版本 → 快速快照覆盖（新值取代所有旧分支）；
 *      - Context 与本地版本并发（离线/过期）→ 标记分支分叉，保留 Sibling。
 */

import { VersionVector } from './version-vector.ts';

export interface VersionedValue {
  value: unknown;
  version: VersionVector;
}

export interface GetResult {
  /** 并发分支；无冲突时长度为 1，键不存在时为 0。 */
  siblings: VersionedValue[];
  /** 客户端后续写入需出示的上下文（所有分支版本的并集）。 */
  context: VersionVector;
}

export type PutOutcome = 'fast-overwrite' | 'sibling-fork';

export interface PutResult {
  outcome: PutOutcome;
  version: VersionVector;
}

export class Replica {
  readonly id: string;
  private readonly store = new Map<string, VersionedValue[]>();

  constructor(id: string) {
    this.id = id;
  }

  get(key: string): GetResult {
    const siblings = this.store.get(key) ?? [];
    const context = siblings.reduce(
      (acc, s) => acc.merge(s.version),
      VersionVector.empty(),
    );
    return { siblings: [...siblings], context };
  }

  /**
   * 写入。context 为客户端上次读取获得的版本向量；
   * 全新客户端（从未读取）传空向量 VersionVector.empty()。
   */
  put(key: string, value: unknown, context: VersionVector): PutResult {
    const existing = this.store.get(key) ?? [];

    // 客户端上下文覆盖（>=）某分支 ⇒ 该分支被本次写入取代；
    // 与上下文并发的分支必须保留（分叉）。
    const superseded = existing.filter((s) => {
      const ord = context.compare(s.version);
      return ord === 'after' || ord === 'equal';
    });
    const surviving = existing.filter((s) => !superseded.includes(s));

    // 新版本的因果历史 = 客户端所见 ∪ 被取代分支的历史，再打上本副本的新事件。
    const base = superseded.reduce(
      (acc, s) => acc.merge(s.version),
      context,
    );
    const version = base.tick(this.id);
    const next: VersionedValue = { value, version };

    // 仍有未被覆盖的并发分支 ⇒ 本次写入来自离线/过期客户端，产生分叉。
    const forked = surviving.length > 0;

    this.store.set(key, [...surviving, next]);
    return { outcome: forked ? 'sibling-fork' : 'fast-overwrite', version };
  }

  /** 上层业务解决冲突后调用：用解决值与合并后的上下文回写，收敛所有分支。 */
  resolve(key: string, value: unknown): PutResult {
    const { context } = this.get(key);
    return this.put(key, value, context);
  }

  /** 供同步器读取的内部快照。 */
  dump(): Map<string, VersionedValue[]> {
    return new Map([...this.store].map(([k, v]) => [k, [...v]]));
  }

  /** 供同步器写入合并结果。 */
  load(key: string, siblings: VersionedValue[]): void {
    if (siblings.length === 0) this.store.delete(key);
    else this.store.set(key, siblings);
  }

  keys(): string[] {
    return [...this.store.keys()];
  }
}
