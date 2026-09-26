import { Rational } from './rational.ts';
import type { ScrollNode } from './scroll-node.ts';
import type {
  LayerRecord,
  ScrollSource,
  ScrollTransaction,
  Vec,
} from './types.ts';
import { vec, vecEq, vecIsZero, vecScale, vecSub, vecZero } from './types.ts';

export interface ScrollToResult {
  readonly applied: Vec;
  readonly blocked: boolean;
  readonly token: string;
}

export interface InertiaOptions {
  readonly decay?: Rational;
  readonly maxSteps?: number;
  readonly originTx?: number;
  readonly epsilon?: Rational;
}

/**
 * 滚动世界：持有节点树，把滚轮事件处理成可重放的嵌套滚动事务。
 * 不变量（每层、逐轴）：received = consumed + passed + discarded。
 */
export class ScrollWorld {
  readonly root: ScrollNode;
  readonly nodes = new Map<string, ScrollNode>();
  readonly transactions: ScrollTransaction[] = [];
  /** 当前捕获容器；非空时滚轮事件改派给它。 */
  captureNodeId: string | null = null;
  private pendingCapture: string | null = null;
  private txSeq = 0;
  private tokenSeq = 0;
  private inFlightTokens = new Set<string>();

  constructor(root: ScrollNode) {
    this.root = root;
    this.register(root);
  }

  private register(node: ScrollNode): void {
    this.nodes.set(node.id, node);
    for (const child of node.children) this.register(child);
  }

  mustGet(id: string): ScrollNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    return node;
  }

  /** 命中测试：返回包含点的最深节点（后挂载的子节点在上层）。 */
  nodeAt(point: Vec): ScrollNode {
    const walk = (node: ScrollNode): ScrollNode | null => {
      if (!node.contains(point)) return null;
      for (let i = node.children.length - 1; i >= 0; i--) {
        const hit = walk(node.children[i]);
        if (hit) return hit;
      }
      return node;
    };
    const hit = walk(this.root);
    if (!hit) throw new Error('point outside root');
    return hit;
  }

  chainFrom(nodeId: string): ScrollNode[] {
    const chain: ScrollNode[] = [];
    let node: ScrollNode | null = this.mustGet(nodeId);
    while (node) {
      chain.push(node);
      node = node.parent;
    }
    return chain;
  }

  /**
   * 派发一次滚轮增量，产生一条事务。
   * 剩余增量沿祖先链传递；捕获容器变化时把剩余量重定向到新捕获链，
   * 已访问节点不会再次消费同一增量。
   */
  dispatchWheel(
    targetId: string,
    delta: Vec,
    source: ScrollSource = { kind: 'user' },
  ): ScrollTransaction {
    const tx: ScrollTransaction = {
      id: ++this.txSeq,
      source,
      targetId,
      initialDelta: delta,
      layers: [],
      escaped: vecZero(),
      preState: this.snapshot(),
      preCapture: this.captureNodeId,
    };
    let chain = this.chainFrom(this.captureNodeId ?? targetId);
    let index = 0;
    let remaining = delta;
    let rerouted = false;
    let transformApplied = false;
    const visited = new Set<string>();

    while (!vecIsZero(remaining) && index < chain.length) {
      const node = chain[index];
      if (visited.has(node.id)) {
        index++;
        continue;
      }
      visited.add(node.id);

      let received = remaining;
      let preTransform: Vec | null = null;
      if (node.axisTransform && !transformApplied) {
        transformApplied = true;
        preTransform = received;
        received = node.axisTransform(received);
      }

      const consumed = node.applyScroll(received, source);
      const rest = vecSub(received, consumed);
      const discarded = vec(
        node.overscroll.x === 'lock' ? rest.x : Rational.zero(),
        node.overscroll.y === 'lock' ? rest.y : Rational.zero(),
      );
      const passed = vecSub(rest, discarded);
      tx.layers.push({ nodeId: node.id, received, preTransform, consumed, passed, discarded, rerouted });
      rerouted = false;
      remaining = passed;

      if (this.pendingCapture !== null) {
        const next = this.pendingCapture;
        this.pendingCapture = null;
        this.captureNodeId = next;
        chain = this.chainFrom(next);
        index = 0;
        rerouted = true;
        continue;
      }
      index++;
    }

    tx.escaped = remaining;
    this.transactions.push(tx);
    return tx;
  }

  /**
   * 程序滚动：回调携带来源令牌；回调内用同一令牌重入会被阻止，
   * 从而打断“程序滚动 → 回调 → 再程序滚动”的反馈环。
   */
  scrollTo(nodeId: string, position: Vec, token?: string): ScrollToResult {
    const useToken = token ?? `prog-${++this.tokenSeq}`;
    if (this.inFlightTokens.has(useToken)) {
      return { applied: vecZero(), blocked: true, token: useToken };
    }
    this.inFlightTokens.add(useToken);
    try {
      const applied = this.mustGet(nodeId).scrollTo(position, { kind: 'programmatic', token: useToken });
      return { applied, blocked: false, token: useToken };
    } finally {
      this.inFlightTokens.delete(useToken);
    }
  }

  /** 监听器用它识别并忽略程序滚动反馈。 */
  isFeedback(source: ScrollSource): boolean {
    return source.kind === 'programmatic' && this.inFlightTokens.has(source.token);
  }

  /** 在滚动回调中请求切换捕获容器；本层记录完成后剩余增量重定向。 */
  requestCapture(nodeId: string): void {
    this.mustGet(nodeId);
    this.pendingCapture = nodeId;
  }

  releaseCapture(): void {
    this.captureNodeId = null;
    this.pendingCapture = null;
  }

  /** 惯性：把衰减的增量序列派生为独立事务，原始增量不会被重复投递。 */
  startInertia(targetId: string, velocity: Vec, options: InertiaOptions = {}): ScrollTransaction[] {
    const decay = options.decay ?? Rational.of('1/2');
    const maxSteps = options.maxSteps ?? 8;
    const epsilon = options.epsilon ?? Rational.zero();
    const origin = options.originTx ?? this.transactions.at(-1)?.id ?? 0;
    const txs: ScrollTransaction[] = [];
    let v = velocity;
    for (let step = 0; step < maxSteps && !vecIsZero(v); step++) {
      if (!epsilon.isZero() && v.x.abs().cmp(epsilon) < 0 && v.y.abs().cmp(epsilon) < 0) break;
      txs.push(this.dispatchWheel(targetId, v, { kind: 'inertia', of: origin }));
      v = vecScale(v, decay);
    }
    return txs;
  }

  snapshot(): Map<string, Vec> {
    const snap = new Map<string, Vec>();
    for (const [id, node] of this.nodes) snap.set(id, node.scroll);
    return snap;
  }

  restore(snap: ReadonlyMap<string, Vec>): void {
    for (const [id, node] of this.nodes) {
      const pos = snap.get(id);
      if (pos) node.scroll = pos;
    }
  }

  /** 重放：回到事务前状态重新执行，校验逐层记录与逃逸量完全一致。 */
  replay(txId: number): { tx: ScrollTransaction; identical: boolean } {
    const original = this.transactions.find((t) => t.id === txId);
    if (!original) throw new Error(`unknown transaction: ${txId}`);
    this.restore(original.preState);
    this.captureNodeId = original.preCapture;
    this.pendingCapture = null;
    const reTx = this.dispatchWheel(original.targetId, original.initialDelta, original.source);
    const identical =
      original.layers.length === reTx.layers.length &&
      original.layers.every((layer, i) => layerEquals(layer, reTx.layers[i])) &&
      vecEq(original.escaped, reTx.escaped);
    return { tx: reTx, identical };
  }
}

function layerEquals(a: LayerRecord, b: LayerRecord): boolean {
  return (
    a.nodeId === b.nodeId &&
    a.rerouted === b.rerouted &&
    vecEq(a.received, b.received) &&
    vecEq(a.consumed, b.consumed) &&
    vecEq(a.passed, b.passed) &&
    vecEq(a.discarded, b.discarded) &&
    ((a.preTransform === null && b.preTransform === null) ||
      (a.preTransform !== null && b.preTransform !== null && vecEq(a.preTransform, b.preTransform)))
  );
}
