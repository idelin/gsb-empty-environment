import { computeEventHash } from './canonical.ts';
import { OwnershipEngine, refOf, type InternalDecision } from './engine.ts';
import { buildGraph } from './graph.ts';
import type {
  Checkpoint,
  Envelope,
  EventDecision,
  KeyResolver,
  LookupEntry,
  Resolution,
} from './types.ts';
import { objectIdKey } from './types.ts';

export interface ResolveOptions {
  /** 受信检查点（压缩前缀），按对象提供。 */
  readonly checkpoints?: readonly Checkpoint[];
  /** 逻辑上界（毫秒）：仅纳入 arrivedAt <= asOf 的信封。 */
  readonly asOf?: number;
}

/**
 * 无缓存解析：每次调用都从完整信封集合重新构建图并判定，
 * 决策引用完整事件哈希链，不依赖任何进程内可变缓存。
 */
export function resolveOwnership(
  envelopes: readonly Envelope[],
  resolver: KeyResolver,
  options: ResolveOptions = {},
): Resolution {
  const asOf = options.asOf ?? Number.POSITIVE_INFINITY;
  const visible = envelopes.filter((envelope) => envelope.arrivedAt <= asOf);

  const malformed: EventDecision[] = [];
  const wellFormed: Envelope[] = [];
  for (const envelope of visible) {
    if (computeEventHash(envelope.body) !== envelope.eventHash) {
      malformed.push({
        eventHash: envelope.eventHash,
        kind: 'reject',
        reason: 'reject_malformed',
        ref: {
          eventHash: envelope.eventHash,
          prev: envelope.body.prev,
          gen: envelope.body.gen,
        },
      });
      continue;
    }
    wellFormed.push(envelope);
  }

  const inbox = buildGraph(wellFormed);
  const engine = new OwnershipEngine(resolver, options.checkpoints ?? []);
  const result = engine.run(inbox);

  const decisionIndex = new Map<string, InternalDecision>();
  for (const decision of result.decisions) decisionIndex.set(decision.eventHash, decision);

  const decisions: EventDecision[] = [];
  const emitted = new Set<string>();
  for (const graph of inbox.objects.values()) {
    for (const node of graph.nodes.values()) {
      const internal = decisionIndex.get(node.hash);
      if (!internal) continue;
      emitted.add(node.hash);
      decisions.push({
        eventHash: node.hash,
        kind: internal.kind,
        reason: internal.reason,
        awaiting: internal.awaiting.length > 0 ? [...internal.awaiting] : undefined,
        ref: refOf(node),
      });
    }
  }
  for (const decision of malformed) {
    decisions.push(decision);
    emitted.add(decision.eventHash);
  }

  // 重复投递：同一原始事件经不同投递者转发，产出 duplicate 观测但不重复判定。
  for (const graph of inbox.objects.values()) {
    for (const node of graph.nodes.values()) {
      if (node.deliveries.length > 1) {
        for (let index = 1; index < node.deliveries.length; index += 1) {
          decisions.push({
            eventHash: node.hash,
            kind: 'publish',
            reason: 'duplicate',
            ref: refOf(node),
          });
        }
      }
    }
  }

  decisions.sort((a, b) => (a.eventHash < b.eventHash ? -1 : a.eventHash > b.eventHash ? 1 : 0));

  return {
    decisions,
    views: result.views,
    isolated: result.isolated,
    duplicateDeliveries: inbox.duplicateDeliveries,
  };
}

/** 时点查询：返回截至 asOf 的判定（等价于带 asOf 的 resolveOwnership）。 */
export function resolveAt(
  envelopes: readonly Envelope[],
  resolver: KeyResolver,
  asOf: number,
  checkpoints: readonly Checkpoint[] = [],
): Resolution {
  return resolveOwnership(envelopes, resolver, { asOf, checkpoints });
}

/**
 * URL/别名索引：同一展示 URL 可被两个不同对象同时声明，因此返回列表。
 * 仅收录当前可发布（未隔离、未分叉）视图，避免旧别名在分叉期间触发部分发布。
 */
export function buildLookupIndex(resolution: Resolution): LookupEntry[] {
  const entries: LookupEntry[] = [];
  for (const [objectKey, view] of resolution.views) {
    if (view.forked || resolution.isolated.includes(objectKey)) continue;
    if (view.displayUrl) {
      entries.push({ objectKey, kind: 'url', value: view.displayUrl });
    }
    for (const alias of view.aliases) {
      entries.push({ objectKey, kind: 'alias', value: alias });
    }
  }
  entries.sort((a, b) => {
    if (a.value !== b.value) return a.value < b.value ? -1 : 1;
    if (a.objectKey !== b.objectKey) return a.objectKey < b.objectKey ? -1 : 1;
    return a.kind < b.kind ? -1 : 1;
  });
  return entries;
}

export { objectIdKey };
