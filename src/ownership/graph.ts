import { computeEventHash } from './canonical.ts';
import { objectIdKey, type Envelope, type GenDigest, type OwnershipEvent } from './types.ts';

/** 同一事件的一次投递记录（transport 只用于审计，不影响判定）。 */
export interface Delivery {
  readonly relay: string | undefined;
  readonly transportSignature: string | undefined;
  readonly arrivedAt: number;
}

export interface EventNode {
  readonly hash: string;
  readonly body: OwnershipEvent;
  readonly auth: Envelope['auth'];
  /** 按到达时间排序的全部投递（含重放）。 */
  readonly deliveries: Delivery[];
  /** 子节点哈希（prev 指向本节点的事件）。 */
  children: string[];
}

export interface ObjectGraph {
  readonly key: string;
  /** hash -> node，包含全部到达事件（无论最终是否发布）。 */
  readonly nodes: Map<string, EventNode>;
  /** create 事件哈希（若已到达）。 */
  root: string | null;
  /** 同代异摘要：gen -> 不同事件哈希集合（分叉/竞争识别依据）。 */
  readonly genDigests: Map<number, Set<string>>;
}

export interface InboxGraph {
  readonly objects: Map<string, ObjectGraph>;
  /** objectKey -> 父对象键（来自 create.parentObject）。 */
  readonly parents: Map<string, string>;
  /** 重复投递次数：同一 eventHash 的额外投递。 */
  duplicateDeliveries: number;
}

/**
 * 无缓存地构建完整事件参考图：不丢弃任何信封，全部以 eventHash 留存。
 * 相同 eventHash（即便经不同投递者、携带不同 transport 签名）合并为同一节点。
 */
export function buildGraph(envelopes: readonly Envelope[]): InboxGraph {
  const objects = new Map<string, ObjectGraph>();
  const parents = new Map<string, string>();
  let duplicateDeliveries = 0;

  const graphOf = (body: OwnershipEvent): ObjectGraph => {
    const key = objectIdKey(body.id);
    let graph = objects.get(key);
    if (!graph) {
      graph = { key, nodes: new Map(), root: null, genDigests: new Map() };
      objects.set(key, graph);
    }
    return graph;
  };

  for (const envelope of envelopes) {
    const computed = computeEventHash(envelope.body);
    if (computed !== envelope.eventHash) {
      // 哈希与事件体不一致：仍保留为独立畸形节点，交由解析器拒绝。
      // 但不允许其冒充正确事件，故以信封声明哈希为主键时会冲突，
      // 这里直接跳过图接入，解析阶段单独产出 reject_malformed。
      continue;
    }
    const graph = graphOf(envelope.body);
    const hash = envelope.eventHash;
    const delivery: Delivery = {
      relay: envelope.delivery?.relay,
      transportSignature: envelope.delivery?.transportSignature,
      arrivedAt: envelope.arrivedAt,
    };
    const existing = graph.nodes.get(hash);
    if (existing) {
      existing.deliveries.push(delivery);
      duplicateDeliveries += 1;
      continue;
    }
    const node: EventNode = {
      hash,
      body: envelope.body,
      auth: envelope.auth,
      deliveries: [delivery],
      children: [],
    };
    graph.nodes.set(hash, node);
    if (envelope.body.type === 'create') {
      graph.root = hash;
      if (envelope.body.parentObject) {
        parents.set(graph.key, objectIdKey(envelope.body.parentObject));
      }
    }
    let digestSet = graph.genDigests.get(envelope.body.gen);
    if (!digestSet) {
      digestSet = new Set();
      graph.genDigests.set(envelope.body.gen, digestSet);
    }
    digestSet.add(hash);
  }

  for (const graph of objects.values()) {
    for (const node of graph.nodes.values()) {
      if (node.body.prev && graph.nodes.has(node.body.prev)) {
        graph.nodes.get(node.body.prev)!.children.push(node.hash);
      }
    }
  }

  return { objects, parents, duplicateDeliveries };
}

export function snapshotGenDigests(graph: ObjectGraph): GenDigest[] {
  return [...graph.genDigests.entries()]
    .filter(([, digests]) => digests.size > 1)
    .map(([gen, digests]) => ({ gen, digests: [...digests].sort() }))
    .sort((a, b) => a.gen - b.gen);
}

/** 收集某对象的全部后代键（基于 create.parentObject 形成的包含树）。 */
export function descendantKeys(graph: InboxGraph, objectKey: string): string[] {
  const result: string[] = [];
  const walk = (parent: string): void => {
    for (const [child, childParent] of graph.parents) {
      if (childParent === parent) {
        result.push(child);
        walk(child);
      }
    }
  };
  walk(objectKey);
  return result;
}
