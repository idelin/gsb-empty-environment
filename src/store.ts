import type { QueryResult, RecordInput, Revision } from "./types.ts";

/**
 * 公共基类：维护版本计数与修订登记，子类分别实现索引查询与逐条扫描参考查询。
 */
export abstract class AbstractStore<A = Record<string, unknown>> {
  protected lastKnowledgeVersion = 0;
  protected revisionSeq = 0;
  protected readonly revisionsById = new Map<number, Revision<A>>();

  /** 当前已分配的最大录入版本 */
  get currentVersion(): number {
    return this.lastKnowledgeVersion;
  }

  /** 录入一条属性修订（含迟到补录：validFrom 可以早于已有数据） */
  record(input: RecordInput<A>): Revision<A> {
    const validTo = input.validTo ?? Infinity;
    if (!(input.validFrom < validTo)) {
      throw new Error(`非法生效区间: [${input.validFrom}, ${validTo})`);
    }
    const knowledgeVersion = input.knowledgeVersion ?? this.lastKnowledgeVersion + 1;
    if (knowledgeVersion < this.lastKnowledgeVersion) {
      throw new Error(
        `录入版本必须单调不减: 收到 ${knowledgeVersion}, 当前 ${this.lastKnowledgeVersion}`,
      );
    }
    this.lastKnowledgeVersion = knowledgeVersion;
    const revision: Revision<A> = {
      fileId: input.fileId,
      revisionId: ++this.revisionSeq,
      validFrom: input.validFrom,
      validTo,
      knowledgeVersion,
      attributes: input.attributes,
    };
    this.revisionsById.set(revision.revisionId, revision);
    this.onRecord(revision);
    return revision;
  }

  /**
   * 撤销指定修订。撤销本身构成一个新的录入版本，
   * 仅对 knowledgeTime >= 撤销版本 的查询隐藏该修订。
   */
  revoke(revisionId: number): Revision<A> {
    const revision = this.revisionsById.get(revisionId);
    if (!revision) {
      throw new Error(`修订不存在: ${revisionId}`);
    }
    if (revision.revokedAt !== undefined) {
      throw new Error(`修订已被撤销: ${revisionId} (版本 ${revision.revokedAt})`);
    }
    revision.revokedAt = ++this.lastKnowledgeVersion;
    return revision;
  }

  /**
   * 双时点查询：业务时点 validTime 落在半开区间 [validFrom, validTo) 内，
   * 且修订在 knowledgeTime 前已录入、未被撤销。
   * 冲突按录入版本再修订 ID 决胜。
   */
  abstract query(fileId: string, validTime: number, knowledgeTime: number): QueryResult<A> | null;

  protected abstract onRecord(revision: Revision<A>): void;

  /** 候选过滤：区间半开、知悉版本不晚于查询时点、查询时点前未被撤销 */
  protected isVisible(revision: Revision<A>, validTime: number, knowledgeTime: number): boolean {
    return (
      revision.validFrom <= validTime &&
      validTime < revision.validTo &&
      revision.knowledgeVersion <= knowledgeTime &&
      (revision.revokedAt === undefined || revision.revokedAt > knowledgeTime)
    );
  }

  /** 决胜规则：录入版本大者优先，相同则修订 ID 大者优先 */
  protected isBetter(candidate: Revision<A>, current: Revision<A>): boolean {
    if (candidate.knowledgeVersion !== current.knowledgeVersion) {
      return candidate.knowledgeVersion > current.knowledgeVersion;
    }
    return candidate.revisionId > current.revisionId;
  }

  protected toResult(revision: Revision<A>): QueryResult<A> {
    return { fileId: revision.fileId, attributes: revision.attributes, revision };
  }
}
