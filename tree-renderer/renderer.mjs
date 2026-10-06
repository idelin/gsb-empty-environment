// renderer.mjs — renderer.ts 的类型擦除孪生（本环境无 tsc，手工保持逐行一致）。

export class StaleCommitError extends Error {}
export class DoubleDisposeError extends Error {}

let nextHostId = 1;

export class VirtualHost {
  constructor() {
    this.stats = {
      createdSubs: 0,
      disposedSubs: 0,
      nodesCreated: 0,
      nodesDisposed: 0,
      doubleDispose: 0,
    };
  }

  createSub(label, version) {
    const stats = this.stats;
    stats.createdSubs++;
    const sub = {
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

  liveSubs() {
    return this.stats.createdSubs - this.stats.disposedSubs;
  }

  createNode(key, type, props) {
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

  disposeNode(node) {
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
  constructor() {
    this.queue = [];
  }

  push(unit) {
    this.queue.push(unit);
  }

  get pending() {
    return this.queue.length;
  }

  step(n = 1) {
    let ran = 0;
    while (ran < n && this.queue.length > 0) {
      this.queue.shift()();
      ran++;
    }
    return ran;
  }

  runUntilIdle() {
    let ran = 0;
    while (this.queue.length > 0) {
      this.queue.shift()();
      ran++;
    }
    return ran;
  }
}

export class Renderer {
  constructor(host, stepper) {
    this.host = host;
    this.stepper = stepper;
    this.latestScheduled = 0;
    this.committedVersion = 0;
    this.prepare = null;
    this.visible = [];
    this.unmounted = false;
  }

  get version() {
    return this.committedVersion;
  }

  get visibleTree() {
    return this.visible;
  }

  // 开始一个新版本的准备；自动取代并清理任何在途 / 待提交的旧准备。
  schedule(version, vnodes) {
    if (this.unmounted) throw new Error('renderer is unmounted');
    if (version <= this.latestScheduled) {
      throw new Error(`version ${version} must be newer than ${this.latestScheduled}`);
    }
    this.latestScheduled = version;
    this.discardPrepare();

    const prepare = { version, root: [], done: false, stale: false, bag: [] };
    this.prepare = prepare;

    // 每个 VNode 的“渲染”是一个工作单元，执行时才继续入队子节点 → 前序增量展开。
    const walk = (list, out) => {
      for (const vnode of list) {
        this.stepper.push(() => {
          if (prepare.stale) return; // 已被取代：剩余工作单元变空操作
          const w = {
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
  commit(version) {
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
  unmount() {
    if (this.unmounted) return;
    this.unmounted = true;
    this.discardPrepare();
    for (const node of this.visible) this.host.disposeNode(node);
    this.visible = [];
  }

  discardPrepare() {
    const p = this.prepare;
    if (!p) return;
    p.stale = true;
    for (const sub of p.bag) sub.dispose();
    p.bag.length = 0;
    this.prepare = null;
  }

  adopt(node, wip, bag) {
    for (const sub of wip.subs) {
      const i = bag.indexOf(sub);
      if (i >= 0) bag.splice(i, 1);
    }
    node.subs = wip.subs;
    wip.subs = [];
  }

  materialize(wip, parent, bag) {
    const node = this.host.createNode(wip.key, wip.type, wip.props);
    node.parent = parent;
    this.adopt(node, wip, bag);
    node.children = wip.children.map((c) => this.materialize(c, node, bag));
    return node;
  }

  reconcile(hostChildren, wipChildren, parent, bag) {
    const byKey = new Map();
    hostChildren.forEach((h, i) => byKey.set(h.key ?? `#${i}`, h));
    const reused = new Set();
    const next = [];

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
