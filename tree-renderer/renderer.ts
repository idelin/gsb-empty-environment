// renderer.ts — 两阶段（准备 / 提交）小型树渲染器。
//
// 不变量：
//  1. 只有“最新且完整”的准备版本可以提交；过期或未完成的提交抛 StaleCommitError。
//  2. 准备被更新的版本取代时，其已创建的订阅立即清理（恰一次）。
//  3. 提交之前不修改可见树；所有可见变更在 commit 内一次性应用。
//  4. unmount 幂等：整棵树与在途准备的订阅各自清理恰一次。

export interface VNode {
  type: string;
  key?: string;
  props?: Record<string, unknown>;
  subs?: string[]; // 该节点渲染期间需要的订阅标签
  children?: VNode[];
}

export interface Sub {
  readonly label: string;
  readonly version: number;
  disposed: boolean;
  dispose(): void;
}

export interface HostNode {
  readonly id: number;
  key: string | null;
  type: string;
  props: Record<string, unknown>;
  children: HostNode[];
  parent: HostNode | null;
  subs: Sub[];
  disposed: boolean;
  disposeCount: number; // 校验“恰一次”用
}

interface WNode {
  key: string | null;
  type: string;
  props: Record<string, unknown>;
  children: WNode[];
  subs: Sub[];
}

interface Prepare {
  version: number;
  root: WNode[];
  done: boolean;
  stale: boolean;
  bag: Sub[]; // 本次准备期间创建、尚未被可见树收编的订阅
}

export class StaleCommitError extends Error {}
export class DoubleDisposeError extends Error {}

let nextHostId = 1;

export class VirtualHost {
  readonly stats = {
    createdSubs: 0,
    disposedSubs: 0,
    nodesCreated: 0,
    nodesDisposed: 0,
    doubleDispose: 0,
  };

  createSub(label: string, version: number): Sub {
    const stats = this.stats;
    stats.createdSubs++;
    const sub: Sub = {
      label,
      version,
      disposed: false,
      dispose() {
        if (sub.disposed) {
          stats.doubleDispose++;
          throw new DoubleDisposeError(`subscription "${label}" disposed twice`);
        }
        sub.disposed = true;
        stats.disposedSubs++;
      },
    };
    return sub;
  }

  liveSubs(): number {
    return this.stats.createdSubs - this.stats.disposedSubs;
  }

  createNode(key: string | null, type: string, props: Record<string, unknown>): HostNode {
    this.stats.nodesCreated++;
    return {
      id: nextHostId++,
      key,
      type,
      props,
      children: [],
      parent: null,
      subs: [],
      disposed: false,
      disposeCount: 0,
    };
  }

  disposeNode(node: HostNode): void {
    if (node.disposed) return; // 节点级幂等护栏
    for (const sub of node.subs) sub.dispose();
    node.subs = [];
    for (const child of [...node.children]) this.disposeNode(child);
    node.children = [];
    node.parent = null;
    node.disposed = true;
    node.disposeCount++;
    this.stats.nodesDisposed++;
  }
}

// 显式步进器：准备工作被切成小单元入队，测试可精确交错“调度 / 执行 / 提交”。
export class Stepper {
  private queue: Array<() => void> = [];

  push(unit: () => void): void {
    this.queue.push(unit);
  }

  get pending(): number {
    return this.queue.length;
  }

  step(n = 1): number {
    let ran = 0;
    while (ran < n && this.queue.length > 0) {
      this.queue.shift()!();
      ran++;
    }
    return ran;
  }

  runUntilIdle(): number {
    let ran = 0;
    while (this.queue.length > 0) {
      this.queue.shift()!();
      ran++;
    }
    return ran;
  }
}

export class Renderer {
  private latestScheduled = 0;
  private committedVersion = 0;
  private prepare: Prepare | null = null;
  private visible: HostNode[] = [];
  private unmounted = false;

  constructor(private readonly host: VirtualHost, private readonly stepper: Stepper) {}

  get version(): number {
    return this.committedVersion;
  }

  get visibleTree(): readonly HostNode[] {
    return this.visible;
  }

  // 开始一个新版本的准备；自动取代并清理任何在途 / 待提交的旧准备。
  schedule(version: number, vnodes: VNode[]): void {
    if (this.unmounted) throw new Error('renderer is unmounted');
    if (version <= this.latestScheduled) {
      throw new Error(`version ${version} must be newer than ${this.latestScheduled}`);
    }
    this.latestScheduled = version;
    this.discardPrepare();

    const prepare: Prepare = { version, root: [], done: false, stale: false, bag: [] };
    this.prepare = prepare;

    // 每个 VNode 的“渲染”是一个工作单元，执行时才继续入队子节点 → 前序增量展开。
    const walk = (list: VNode[], out: WNode[]): void => {
      for (const vnode of list) {
        this.stepper.push(() => {
          if (prepare.stale) return; // 已被取代：剩余工作单元变空操作
          const w: WNode = {
            key: vnode.key ?? null,
            type: vnode.type,
            props: vnode.props ?? {},
            children: [],
            subs: [],
          };
          out.push(w);
          for (const label of vnode.subs ?? []) {
            const sub = this.host.createSub(label, version);
            w.subs.push(sub);
            prepare.bag.push(sub);
          }
          walk(vnode.children ?? [], w.children);
        });
      }
    };
    walk(vnodes, prepare.root);
    this.stepper.push(() => {
      if (!prepare.stale) prepare.done = true;
    });
  }

  // 仅允许提交“当前最新且已完整准备好”的版本。
  commit(version: number): void {
    if (this.unmounted) throw new Error('renderer is unmounted');
    const p = this.prepare;
    if (!p || p.version !== version || p.stale || !p.done) {
      throw new StaleCommitError(
        `cannot commit v${version}: only the latest fully-prepared version may commit`,
      );
    }
    // 提交点：此前可见树从未被准备阶段触碰。
    this.visible = this.reconcile(this.visible, p.root, null, p.bag);
    for (const sub of p.bag) sub.dispose(); // 未被收编的订阅（来自被替换的 WIP 节点）
    p.bag.length = 0;
    this.committedVersion = version;
    this.prepare = null;
  }

  // 幂等卸载：可见树与在途准备的订阅各自恰清理一次。
  unmount(): void {
    if (this.unmounted) return;
    this.unmounted = true;
    this.discardPrepare();
    for (const node of this.visible) this.host.disposeNode(node);
    this.visible = [];
  }

  private discardPrepare(): void {
    const p = this.prepare;
    if (!p) return;
    p.stale = true;
    for (const sub of p.bag) sub.dispose();
    p.bag.length = 0;
    this.prepare = null;
  }

  private adopt(node: HostNode, wip: WNode, bag: Sub[]): void {
    for (const sub of wip.subs) {
      const i = bag.indexOf(sub);
      if (i >= 0) bag.splice(i, 1);
    }
    node.subs = wip.subs;
    wip.subs = [];
  }

  private materialize(wip: WNode, parent: HostNode | null, bag: Sub[]): HostNode {
    const node = this.host.createNode(wip.key, wip.type, wip.props);
    node.parent = parent;
    this.adopt(node, wip, bag);
    node.children = wip.children.map((c) => this.materialize(c, node, bag));
    return node;
  }

  private reconcile(
    hostChildren: HostNode[],
    wipChildren: WNode[],
    parent: HostNode | null,
    bag: Sub[],
  ): HostNode[] {
    const byKey = new Map<string, HostNode>();
    hostChildren.forEach((h, i) => byKey.set(h.key ?? `#${i}`, h));
    const reused = new Set<HostNode>();
    const next: HostNode[] = [];

    wipChildren.forEach((wip, i) => {
      const match = byKey.get(wip.key ?? `#${i}`);
      if (match && match.type === wip.type) {
        reused.add(match);
        match.parent = parent;
        match.props = wip.props;
        for (const sub of match.subs) sub.dispose(); // 旧订阅被新准备的订阅替换
        match.subs = [];
        this.adopt(match, wip, bag);
        match.children = this.reconcile(match.children, wip.children, match, bag);
        next.push(match);
      } else {
        next.push(this.materialize(wip, parent, bag));
      }
    });

    for (const h of hostChildren) {
      if (!reused.has(h)) this.host.disposeNode(h);
    }
    return next;
  }
}
