/**
 * B+ 树 + 螃蟹脚并发加锁协议（Latch Crabbing / Coupling）。
 *
 * 协议规则（自顶向下）：
 *  - 查找：先持有父节点读闩，再获取子节点读闩，随后立即释放父节点读闩
 *    （"coupling"，任意时刻最多持有 2 个节点闩）。
 *  - 插入：悲观地以写闩下行。每闩住一个子节点后检查其是否"安全"
 *    （keys 未满，本次插入不会引发分裂）；若安全，则释放所有祖先的写闩，
 *    因为分裂不可能向上传播越过安全节点。
 *  - 无死锁：所有任务都严格按 "rootLatch -> 父 -> 子" 的同一偏序获取闩，
 *    等待图中不可能成环。
 *
 * 根指针处理：根指针由树级 rootLatch 保护（对应 bustub 的 root_page_id 闩）。
 * 读/写任务都只在"抓取根指针 + 闩住根节点"的短暂瞬间持有 rootLatch 读闩，
 * 随后立即释放，因此高层未分裂节点上的读操作几乎从不阻塞。
 * 当根已满（可能分裂）时，写线程先把 rootLatch 升级为写闩、
 * 预防性地分裂根（树高 +1），再以读闩下行 —— 根分裂不会成为
 * 螃蟹脚链条的一部分，等待图依然无环。
 */

import { ReadWriteLatch, type LatchStats } from "./latch.ts";

export class BPlusNode {
  isLeaf: boolean;
  keys: number[] = [];
  /** 仅内部节点使用，长度恒为 keys.length + 1 */
  children: BPlusNode[] = [];
  readonly latch = new ReadWriteLatch();

  constructor(isLeaf: boolean) {
    this.isLeaf = isLeaf;
  }
}

export interface TreeOptions {
  /** 每个节点最多容纳的 key 数（默认 8） */
  maxKeys?: number;
  /**
   * 测试/演示用：在每层下行之间主动让出事件循环，
   * 放大任务交错，从而真正制造闩竞争。
   */
  interleave?: boolean;
}

export interface LevelStats extends LatchStats {
  level: number;
  nodes: number;
}

/** keys 中 <= key 的元素个数，即 B+ 树内部节点应下行的孩子下标 */
function childIndex(keys: number[], key: number): number {
  let lo = 0;
  let hi = keys.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (keys[mid]! <= key) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/** keys 中第一个 >= key 的下标 */
function lowerBound(keys: number[], key: number): number {
  let lo = 0;
  let hi = keys.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (keys[mid]! < key) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

const yieldNow = (): Promise<void> =>
  new Promise((resolve) => setImmediate(resolve));

export class BPlusTree {
  /** 当前根节点，仅由 rootLatch 保护 */
  private root: BPlusNode = new BPlusNode(true);
  /** 树级闩：保护根指针；仅在根需要预防性分裂时才以写模式持有 */
  private readonly rootLatch = new ReadWriteLatch();
  private readonly maxKeys: number;
  private readonly interleave: boolean;

  // ---- 可观测性（验证螃蟹脚协议行为）----
  /** 单次查找过程中同时持有的节点闩峰值（读耦合应恒 <= 2） */
  maxHeldDuringFind = 0;
  /** 查找到达叶子时仍持有的节点闩数（读耦合应恒为 1） */
  maxHeldAtLeafFind = 0;
  /** 插入到达叶子时仍持有的节点闩数最小值（安全路径应能降到 1） */
  minHeldAtLeafInsert = Number.POSITIVE_INFINITY;
  /** 插入过程中同时持有的节点闩峰值 */
  maxHeldDuringInsert = 0;
  /** 根预防性分裂（树高增长）次数 */
  rootSplits = 0;

  constructor(options: TreeOptions = {}) {
    this.maxKeys = options.maxKeys ?? 8;
    if (this.maxKeys < 2) throw new Error("maxKeys must be >= 2");
    this.interleave = options.interleave ?? false;
  }

  private async maybeYield(): Promise<void> {
    if (this.interleave) await yieldNow();
  }

  /** 插入语义下的"安全节点"：再插入一个 key 也不会分裂 */
  private isSafeForInsert(node: BPlusNode): boolean {
    return node.keys.length < this.maxKeys;
  }

  /** 闩住当前根节点（读模式），返回根节点；rootLatch 随即释放 */
  private async latchRootForRead(): Promise<BPlusNode> {
    await this.rootLatch.acquireRead();
    const root = this.root;
    await root.latch.acquireRead();
    this.rootLatch.releaseRead();
    return root;
  }

  // ---------------------------------------------------------------- 查找

  /** 读耦合：闩住子节点后立刻释放父节点，任意时刻最多持有 2 个读闩 */
  async find(key: number): Promise<boolean> {
    let held = 0;
    let heldPeak = 0;
    const acquire = async (node: BPlusNode): Promise<void> => {
      await node.latch.acquireRead();
      held++;
      heldPeak = Math.max(heldPeak, held);
    };
    const release = (node: BPlusNode): void => {
      node.latch.releaseRead();
      held--;
    };

    let node = await this.latchRootForRead();
    held = 1;
    heldPeak = 1;
    while (!node.isLeaf) {
      const child = node.children[childIndex(node.keys, key)]!;
      await acquire(child); // 先拿子闩
      release(node); // 再放父闩 —— 高层节点被即刻释放
      node = child;
      await this.maybeYield();
    }
    this.maxHeldDuringFind = Math.max(this.maxHeldDuringFind, heldPeak);
    this.maxHeldAtLeafFind = Math.max(this.maxHeldAtLeafFind, held);

    const idx = lowerBound(node.keys, key);
    const found = idx < node.keys.length && node.keys[idx] === key;
    release(node);
    return found;
  }

  // ---------------------------------------------------------------- 插入

  /** 悲观写耦合：子节点安全则释放全部祖先写闩；返回 false 表示 key 已存在 */
  async insert(key: number): Promise<boolean> {
    const root = await this.latchRootForInsert();

    // 从根开始悲观下行；held 保存仍持有写闩的祖先链
    const held: BPlusNode[] = [root];
    let heldPeak = 1;
    let node = root;

    while (!node.isLeaf) {
      const child = node.children[childIndex(node.keys, key)]!;
      await child.latch.acquireWrite();
      if (this.isSafeForInsert(child)) {
        // 子节点不会分裂 => 祖先不可能再被波及，提前全部释放
        for (const ancestor of held) ancestor.latch.releaseWrite();
        held.length = 0;
      }
      held.push(child);
      heldPeak = Math.max(heldPeak, held.length);
      node = child;
      await this.maybeYield();
    }
    this.maxHeldDuringInsert = Math.max(this.maxHeldDuringInsert, heldPeak);
    this.minHeldAtLeafInsert = Math.min(this.minHeldAtLeafInsert, held.length);

    // node 为叶子，且从最近的不安全祖先到叶子的整条链都仍持有写闩
    const idx = lowerBound(node.keys, key);
    if (idx < node.keys.length && node.keys[idx] === key) {
      for (const n of held) n.latch.releaseWrite();
      return false; // 重复 key
    }
    node.keys.splice(idx, 0, key);

    // 分裂自底向上传播；所有可能受影响的祖先都在 held 中且已加写闩。
    // 根已被保证不满（见 latchRootForInsert），传播必然终止于 held 之内。
    while (node.keys.length > this.maxKeys) {
      const parent = held[held.length - 2]!;
      const { upKey, right } = this.splitNode(node);
      const pos = childIndex(parent.keys, upKey);
      parent.keys.splice(pos, 0, upKey);
      parent.children.splice(pos + 1, 0, right);
      held.pop();
      node.latch.releaseWrite();
      node = parent;
    }
    for (const n of held) n.latch.releaseWrite();
    return true;
  }

  /**
   * 以写模式闩住根节点并返回。若根已满，先在 rootLatch 写闩保护下
   * 预防性分裂根（树高 +1），保证之后的下行中根不可能再分裂，
   * 因此分裂传播永远不会需要"根的父节点"。
   */
  private async latchRootForInsert(): Promise<BPlusNode> {
    for (;;) {
      // 快路径：rootLatch 读闩保护下抓住根指针并闩住根节点。
      // 必须在持有根节点写闩之后复检"根是否已满"——此前根可能
      // 被其他写线程填满。
      await this.rootLatch.acquireRead();
      const root = this.root;
      await root.latch.acquireWrite();
      this.rootLatch.releaseRead();
      if (root.keys.length < this.maxKeys) return root;
      root.latch.releaseWrite();

      // 慢路径：根已满，升级为 rootLatch 写闩并预防性分裂（树高 +1）。
      // 关键点：等待 rootLatch 写闩期间不持有任何节点闩；持有
      // rootLatch 写闩时只等待根节点闩，而根节点闩的持有者只会
      // 向下等待——等待图无环，不会死锁。
      await this.rootLatch.acquireWrite();
      if (this.root.keys.length >= this.maxKeys) {
        const oldRoot = this.root;
        await oldRoot.latch.acquireWrite();
        const { upKey, right } = this.splitNode(oldRoot);
        const newRoot = new BPlusNode(false);
        newRoot.keys.push(upKey);
        newRoot.children.push(oldRoot, right);
        this.root = newRoot;
        this.rootSplits++;
        oldRoot.latch.releaseWrite();
      }
      this.rootLatch.releaseWrite();
      // 回到快路径重新闩住（新）根
    }
  }

  /** 分裂已满（maxKeys+1 个 key）的节点，返回上推 key 与新的右兄弟 */
  private splitNode(node: BPlusNode): { upKey: number; right: BPlusNode } {
    const right = new BPlusNode(node.isLeaf);
    const total = node.keys.length;
    if (node.isLeaf) {
      // B+ 树叶子：复制中间 key 上推，右兄弟保留它
      const mid = Math.ceil(total / 2);
      right.keys = node.keys.splice(mid);
      return { upKey: right.keys[0]!, right };
    }
    // 内部节点：中间 key 上推，不保留在任何孩子中
    const mid = Math.floor(total / 2);
    const upKey = node.keys[mid]!;
    right.keys = node.keys.splice(mid + 1);
    node.keys.pop();
    right.children = node.children.splice(mid + 1);
    return { upKey, right };
  }

  // ------------------------------------------------------------ 校验/统计

  /** 静态校验树结构不变量（仅允许在无并发操作时调用） */
  validate(): void {
    this.validateNode(
      this.root,
      0,
      Number.NEGATIVE_INFINITY,
      Number.POSITIVE_INFINITY,
      true,
    );
  }

  private validateNode(
    node: BPlusNode,
    depth: number,
    lo: number,
    hi: number,
    isRoot: boolean,
  ): number {
    for (let i = 0; i < node.keys.length; i++) {
      if (i > 0 && node.keys[i]! <= node.keys[i - 1]!) {
        throw new Error(`keys not strictly increasing at depth ${depth}`);
      }
      if (node.keys[i]! < lo || node.keys[i]! >= hi) {
        throw new Error(`key ${node.keys[i]} out of range [${lo}, ${hi})`);
      }
    }
    if (!isRoot && node.keys.length > this.maxKeys) {
      throw new Error("node overflow");
    }
    if (node.isLeaf) return depth;
    if (node.children.length !== node.keys.length + 1) {
      throw new Error("internal node children/keys mismatch");
    }
    let leafDepth = -1;
    for (let i = 0; i < node.children.length; i++) {
      const childLo = i === 0 ? lo : node.keys[i - 1]!;
      const childHi = i === node.children.length - 1 ? hi : node.keys[i]!;
      const d = this.validateNode(
        node.children[i]!,
        depth + 1,
        childLo,
        childHi,
        false,
      );
      if (leafDepth === -1) leafDepth = d;
      else if (d !== leafDepth) throw new Error("leaves at different depths");
    }
    return leafDepth;
  }

  /** key 总数（仅允许在无并发操作时调用） */
  size(): number {
    let count = 0;
    const stack: BPlusNode[] = [this.root];
    while (stack.length > 0) {
      const node = stack.pop()!;
      if (node.isLeaf) count += node.keys.length;
      else stack.push(...node.children);
    }
    return count;
  }

  /** 按层聚合闩统计（仅允许在无并发操作时调用），level 0 为根层 */
  statsByLevel(): LevelStats[] {
    const levels: LevelStats[] = [];
    let frontier: BPlusNode[] = [this.root];
    let level = 0;
    while (frontier.length > 0) {
      const agg: LevelStats = {
        level,
        nodes: frontier.length,
        readImmediate: 0,
        readWait: 0,
        writeImmediate: 0,
        writeWait: 0,
      };
      const next: BPlusNode[] = [];
      for (const node of frontier) {
        agg.readImmediate += node.latch.stats.readImmediate;
        agg.readWait += node.latch.stats.readWait;
        agg.writeImmediate += node.latch.stats.writeImmediate;
        agg.writeWait += node.latch.stats.writeWait;
        if (!node.isLeaf) next.push(...node.children);
      }
      levels.push(agg);
      frontier = next;
      level++;
    }
    return levels;
  }

  /** 树级 rootLatch 的统计 */
  get rootLatchStats(): LatchStats {
    return this.rootLatch.stats;
  }
}
