import type { KvBackend } from "./backends.ts";
import { MemoryBackend } from "./backends.ts";

/** 命名空间分类：分区缓存（嵌入方默认）与未分区（顶层）存储。 */
export type NamespaceClass = "partitioned" | "unpartitioned";

/** 授权规则：只读或读写。 */
export type GrantRule = "read-only" | "read-write";

/** 存储操作种类。 */
export type OperationType = "get" | "set" | "remove" | "has";

/** 事务的最终确定归属。 */
export type TransactionStatus =
  | "committed"
  | "denied-no-grant"
  | "denied-revoked"
  | "denied-read-only"
  | "denied-stale"
  | "denied-context";

/** 嵌入文档所处的安全上下文，等价于 Storage Access API 的身份三元组。 */
export interface StorageContext {
  /** 顶层站点（注册域 + scheme 的 eTLD+1 抽象）。 */
  topLevelSite: string;
  /** 嵌入文档自身的来源。 */
  embedderOrigin: string;
  /** 文档代：每次导航（含同站导航）单调递增。 */
  documentGeneration: number;
}

/** 一条已签发的授权，身份三元组与命名空间在签发时冻结。 */
export interface Grant extends StorageContext {
  grantId: string;
  namespace: string;
  rule: GrantRule;
  issuedAt: number;
  revoked: boolean;
  revokedAt: number | null;
}

/** 授权请求参数。 */
export interface GrantRequest {
  topLevelSite: string;
  embedderOrigin: string;
  /**
   * 授权时选定的命名空间键。授权一经签发，后续读写固定落在该命名空间，
   * 不存在“合并分区与未分区”的路径。
   */
  namespace: string;
  rule?: GrantRule;
}

/** 提交给用户代理/用户的授权提示上下文。 */
export interface PromptContext {
  topLevelSite: string;
  embedderOrigin: string;
  namespace: string;
  rule: GrantRule;
  documentGeneration: number;
}

/** 授权提示处理器：返回 false 表示用户拒绝。 */
export type PromptHandler = (context: PromptContext) => boolean | Promise<boolean>;

/** 每个已落定事务的审计记录，归属在入队时刻即确定。 */
export interface TransactionRecord {
  transactionId: string;
  grantId: string | null;
  operation: OperationType;
  key: string;
  /** 实际写入/读取的命名空间全限定键的命名空间部分。 */
  namespace: string | null;
  namespaceClass: NamespaceClass | null;
  status: TransactionStatus;
  /** 入队时捕获的文档代，用于判定旧事务。 */
  enqueuedGeneration: number;
  /** 执行时的当前文档代。 */
  executedGeneration: number;
  at: number;
}

export interface StorageResult {
  ok: boolean;
  status: TransactionStatus;
  transactionId: string;
}

export interface StorageReadResult extends StorageResult {
  value: string | null;
}

export interface StorageHasResult extends StorageResult {
  has: boolean;
}

export interface ManagerOptions {
  /** 未分区（顶层）存储后端。 */
  unpartitioned?: KvBackend;
  /** 分区缓存后端；缺省时每个 accessor 内部独立持有一个 MemoryBackend。 */
  partitioned?: KvBackend;
  /** 授权提示处理器，默认一律允许（仅适合测试）。 */
  prompt?: PromptHandler;
  now?: () => number;
  idFactory?: () => string;
}

/** 请求被用户拒绝时抛出。 */
export class StorageAccessDeniedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "StorageAccessDeniedError";
  }
}

/** 拼接命名空间内的全限定键，确保不同命名空间物理隔离。 */
export function namespacedKey(namespace: string, key: string): string {
  return `ns:${namespace}::${key}`;
}

interface ExecutionPlan {
  grant: Grant | null;
  status: TransactionStatus;
  allowed: boolean;
  namespace: string | null;
  namespaceClass: NamespaceClass | null;
  enqueuedGeneration: number;
}

/** 入队时刻可确定的静态失败：缺少授权、身份三元组不匹配、只读违规。 */
function staticPlan(
  grant: Grant | null,
  operation: OperationType,
  enqueued: StorageContext,
): ExecutionPlan {
  const base = {
    grant,
    enqueuedGeneration: enqueued.documentGeneration,
    namespace: null as string | null,
    namespaceClass: null as NamespaceClass | null,
  };
  if (!grant) {
    return { ...base, allowed: false, status: "denied-no-grant" };
  }
  if (
    grant.topLevelSite !== enqueued.topLevelSite ||
    grant.embedderOrigin !== enqueued.embedderOrigin
  ) {
    return { ...base, allowed: false, status: "denied-context" };
  }
  const mutating = operation === "set" || operation === "remove";
  if (mutating && grant.rule === "read-only") {
    return {
      ...base,
      allowed: false,
      status: "denied-read-only",
      namespace: grant.namespace,
      namespaceClass: "unpartitioned",
    };
  }
  return {
    ...base,
    allowed: true,
    status: "committed",
    namespace: grant.namespace,
    namespaceClass: "unpartitioned",
  };
}

/**
 * 存储访问策略中心：持有两类后端、授权表与事务审计日志。
 * 所有状态变更串行化，保证并行授权/写入的确定顺序。
 */
export class StorageAccessManager {
  readonly #unpartitioned: KvBackend;
  readonly #partitionedFallback: KvBackend;
  readonly #grants = new Map<string, Grant>();
  readonly #inflight = new Map<string, Promise<Grant>>();
  readonly #ledger: TransactionRecord[] = [];
  readonly #prompt: PromptHandler;
  readonly #now: () => number;
  readonly #idFactory: () => string;
  #idCounter = 0;

  constructor(options: ManagerOptions = {}) {
    this.#unpartitioned = options.unpartitioned ?? new MemoryBackend();
    this.#partitionedFallback = options.partitioned ?? new MemoryBackend();
    this.#prompt = options.prompt ?? (() => true);
    this.#now = options.now ?? (() => Date.now());
    this.#idFactory =
      options.idFactory ??
      (() => {
        this.#idCounter += 1;
        return `id-${this.#idCounter}`;
      });
  }

  get ledger(): readonly TransactionRecord[] {
    return this.#ledger;
  }

  get unpartitionedBackend(): KvBackend {
    return this.#unpartitioned;
  }

  getGrant(grantId: string): Grant | undefined {
    const grant = this.#grants.get(grantId);
    return grant ? { ...grant } : undefined;
  }

  listGrants(): Grant[] {
    return [...this.#grants.values()].map((grant) => ({ ...grant }));
  }

  /** 为一个嵌入文档创建访问器，绑定其初始安全上下文与分区缓存。 */
  createAccessor(
    context: Omit<StorageContext, "documentGeneration"> & { documentGeneration?: number },
  ): EmbeddedStorageAccessor {
    return new EmbeddedStorageAccessor(
      this,
      this.#partitionedFallback,
      {
        topLevelSite: context.topLevelSite,
        embedderOrigin: context.embedderOrigin,
        documentGeneration: context.documentGeneration ?? 0,
      },
    );
  }

  /**
   * 签发授权。相同身份三元组 + 命名空间 + 规则的并行请求共用同一个
   * in-flight Promise，提示处理器只会被调用一次。
   */
  async requestGrant(request: GrantRequest, context: StorageContext): Promise<Grant> {
    const rule: GrantRule = request.rule ?? "read-write";
    const dedupKey = [
      context.topLevelSite,
      context.embedderOrigin,
      context.documentGeneration,
      request.namespace,
      rule,
    ].join("|");

    const inflight = this.#inflight.get(dedupKey);
    if (inflight) {
      return inflight;
    }

    const promise = this.#issueGrant(request, context, rule).finally(() => {
      this.#inflight.delete(dedupKey);
    });
    this.#inflight.set(dedupKey, promise);
    return promise;
  }

  async #issueGrant(request: GrantRequest, context: StorageContext, rule: GrantRule): Promise<Grant> {
    const approved = await this.#prompt({
      topLevelSite: context.topLevelSite,
      embedderOrigin: context.embedderOrigin,
      namespace: request.namespace,
      rule,
      documentGeneration: context.documentGeneration,
    });
    if (!approved) {
      throw new StorageAccessDeniedError(
        `存储访问被拒绝：${context.embedderOrigin} @ ${context.topLevelSite}#${context.documentGeneration}`,
      );
    }

    const grant: Grant = {
      grantId: this.#idFactory(),
      topLevelSite: context.topLevelSite,
      embedderOrigin: context.embedderOrigin,
      documentGeneration: context.documentGeneration,
      namespace: request.namespace,
      rule,
      issuedAt: this.#now(),
      revoked: false,
      revokedAt: null,
    };
    this.#grants.set(grant.grantId, grant);
    return { ...grant };
  }

  /**
   * 撤销授权：仅翻转授权状态。不读取、不复制、不删除任何后端数据，
   * 未分区值绝不会因此被搬回分区缓存。
   */
  revokeGrant(grantId: string): boolean {
    const grant = this.#grants.get(grantId);
    if (!grant || grant.revoked) {
      return false;
    }
    grant.revoked = true;
    grant.revokedAt = this.#now();
    return true;
  }

  async #run(
    accessor: EmbeddedStorageAccessor,
    grant: Grant | null,
    operation: OperationType,
    key: string,
    task: (backend: KvBackend, fullKey: string) => Promise<void> | void,
  ): Promise<TransactionRecord> {
    // 同步捕获入队时刻的上下文与计划：身份与文档代归属从此冻结。
    const enqueuedContext = accessor.snapshotContext();
    const enqueuedPlan = staticPlan(grant, operation, enqueuedContext);

    return accessor._enqueue(async () => {
      // 动态安全状态（文档代导航、撤销）在出队执行时刻对照当前上下文重评。
      let plan = enqueuedPlan;
      if (enqueuedPlan.allowed && grant) {
        const current = accessor.snapshotContext();
        const live = this.#grants.get(grant.grantId);
        if (live?.revoked) {
          plan = { ...enqueuedPlan, allowed: false, status: "denied-revoked" };
        } else if (grant.documentGeneration !== current.documentGeneration) {
          plan = { ...enqueuedPlan, allowed: false, status: "denied-stale" };
        }
      }
      const record: TransactionRecord = {
        transactionId: this.#idFactory(),
        grantId: plan.grant?.grantId ?? null,
        operation,
        key,
        namespace: plan.namespace,
        namespaceClass: plan.namespaceClass,
        status: plan.status,
        enqueuedGeneration: plan.enqueuedGeneration,
        executedGeneration: accessor.snapshotContext().documentGeneration,
        at: this.#now(),
      };

      if (plan.allowed && plan.namespace !== null) {
        await task(this.#unpartitioned, namespacedKey(plan.namespace, key));
      }
      this.#ledger.push(record);
      return record;
    });
  }

  async get(accessor: EmbeddedStorageAccessor, key: string): Promise<StorageReadResult> {
    const grant = accessor.boundGrant;
    const record = await this.#run(accessor, grant, "get", key, () => {});
    if (record.status !== "committed") {
      return { ok: false, status: record.status, transactionId: record.transactionId, value: null };
    }
    const value = await this.#unpartitioned.get(namespacedKey(record.namespace!, key));
    return { ok: true, status: "committed", transactionId: record.transactionId, value };
  }

  async has(accessor: EmbeddedStorageAccessor, key: string): Promise<StorageHasResult> {
    const grant = accessor.boundGrant;
    const record = await this.#run(accessor, grant, "has", key, () => {});
    if (record.status !== "committed") {
      return { ok: false, status: record.status, transactionId: record.transactionId, has: false };
    }
    const has = await this.#unpartitioned.has(namespacedKey(record.namespace!, key));
    return { ok: true, status: "committed", transactionId: record.transactionId, has };
  }

  async set(accessor: EmbeddedStorageAccessor, key: string, value: string): Promise<StorageResult> {
    const grant = accessor.boundGrant;
    const record = await this.#run(accessor, grant, "set", key, (backend, fullKey) =>
      backend.set(fullKey, value),
    );
    return {
      ok: record.status === "committed",
      status: record.status,
      transactionId: record.transactionId,
    };
  }

  async remove(accessor: EmbeddedStorageAccessor, key: string): Promise<StorageResult> {
    const grant = accessor.boundGrant;
    const record = await this.#run(accessor, grant, "remove", key, (backend, fullKey) =>
      backend.remove(fullKey),
    );
    return {
      ok: record.status === "committed",
      status: record.status,
      transactionId: record.transactionId,
    };
  }
}

/**
 * 单个嵌入文档的存储访问句柄。
 *
 * 未授权时读写落在自身分区缓存；授权后固定使用授权时选定的命名空间
 * （未分区后端）。两条数据路径物理隔离，组件不提供任何合并/迁移 API，
 * 撤销也不会把未分区值复制回分区缓存。
 */
export class EmbeddedStorageAccessor {
  readonly #manager: StorageAccessManager;
  readonly #partitioned: KvBackend;
  #context: StorageContext;
  #boundGrant: Grant | null = null;
  #chain: Promise<unknown> = Promise.resolve();

  constructor(
    manager: StorageAccessManager,
    partitioned: KvBackend,
    context: StorageContext,
  ) {
    this.#manager = manager;
    this.#partitioned = partitioned;
    this.#context = { ...context };
  }

  get topLevelSite(): string {
    return this.#context.topLevelSite;
  }

  get embedderOrigin(): string {
    return this.#context.embedderOrigin;
  }

  get documentGeneration(): number {
    return this.#context.documentGeneration;
  }

  /** 当前绑定的授权；导航/撤销后可能为 null 或已失效的旧授权。 */
  get boundGrant(): Readonly<Grant> | null {
    return this.#boundGrant ? { ...this.#boundGrant } : null;
  }

  /** 供管理器在入队时刻同步捕获身份三元组。 */
  snapshotContext(): StorageContext {
    return { ...this.#context };
  }

  /**
   * 串行队列：同一访问器上的授权后读写与分区缓存操作按入队顺序执行，
   * 并行调用的结果顺序由此确定。
   */
  _enqueue<T>(task: () => Promise<T> | T): Promise<T> {
    const result = this.#chain.then(() => task());
    // 隔离单个任务失败，避免队列被永久毒化。
    this.#chain = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  /**
   * 导航：文档代单调递增，可同时更新顶层站点。
   * 旧授权保留在绑定上以便审计其失效（denied-stale），但不再可用于新事务。
   */
  navigate(next: { topLevelSite?: string } = {}): number {
    this.#context = {
      topLevelSite: next.topLevelSite ?? this.#context.topLevelSite,
      embedderOrigin: this.#context.embedderOrigin,
      documentGeneration: this.#context.documentGeneration + 1,
    };
    return this.#context.documentGeneration;
  }

  /**
   * 请求存储访问授权并绑定到当前文档代。
   * 并行的相同请求由管理器去重，只会产生一次用户提示。
   */
  async requestAccess(
    request: Omit<GrantRequest, "topLevelSite" | "embedderOrigin"> &
      Partial<Pick<GrantRequest, "topLevelSite" | "embedderOrigin">>,
  ): Promise<Grant> {
    const context = this.snapshotContext();
    const grant = await this.#manager.requestGrant(
      {
        topLevelSite: request.topLevelSite ?? context.topLevelSite,
        embedderOrigin: request.embedderOrigin ?? context.embedderOrigin,
        namespace: request.namespace,
        rule: request.rule,
      },
      context,
    );
    this.#boundGrant = grant;
    return { ...grant };
  }

  /**
   * 撤销当前绑定授权：只翻转授权状态并解除绑定。
   * 不触碰任何后端数据，未分区值保持原位，分区缓存保持原样。
   */
  revokeAccess(): boolean {
    if (!this.#boundGrant) {
      return false;
    }
    const grantId = this.#boundGrant.grantId;
    const revoked = this.#manager.revokeGrant(grantId);
    this.#boundGrant = null;
    return revoked;
  }

  /** 授权后读：固定走授权命名空间；无有效授权时返回拒绝结果而非回落分区缓存。 */
  get(key: string): Promise<StorageReadResult> {
    return this.#manager.get(this, key);
  }

  has(key: string): Promise<StorageHasResult> {
    return this.#manager.has(this, key);
  }

  set(key: string, value: string): Promise<StorageResult> {
    return this.#manager.set(this, key, value);
  }

  remove(key: string): Promise<StorageResult> {
    return this.#manager.remove(this, key);
  }

  /** 未授权场景下的分区缓存读写，始终与未分区后端隔离。 */
  getPartitioned(key: string): Promise<string | null> {
    return this._enqueue(() => this.#partitioned.get(key));
  }

  setPartitioned(key: string, value: string): Promise<void> {
    return this._enqueue(() => this.#partitioned.set(key, value));
  }

  removePartitioned(key: string): Promise<void> {
    return this._enqueue(() => this.#partitioned.remove(key));
  }

  hasPartitioned(key: string): Promise<boolean> {
    return this._enqueue(() => this.#partitioned.has(key));
  }
}
