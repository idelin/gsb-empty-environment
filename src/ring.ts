import type { HashFn, Key } from './hashes.ts';

export interface RingStats {
  nodes: number;
  vnodesPerNode: number;
  totalPoints: number;
  distinctPositions: number;
  /** 虚拟节点落点发生碰撞（位置完全相同）的次数 */
  collisions: number;
}

/** 一致性哈希环：虚拟节点 + 排序位置数组 + 二分查找 */
export class HashRing {
  readonly hashFn: HashFn;
  readonly vnodes: number;
  /** 环地址空间大小（哈希值上界，截断位宽时为 2^bits） */
  readonly space: number;
  readonly stats: RingStats;
  private positions: number[] = [];
  private owners: string[] = [];

  constructor(nodes: string[], hashFn: HashFn, vnodes = 160, space = 2 ** 32) {
    if (nodes.length === 0) throw new Error('HashRing: nodes 不能为空');
    this.hashFn = hashFn;
    this.vnodes = vnodes;
    this.space = space;

    const points: { pos: number; owner: string }[] = [];
    for (const node of nodes) {
      for (let i = 0; i < vnodes; i++) {
        points.push({ pos: hashFn(`${node}#${i}`), owner: node });
      }
    }
    points.sort((a, b) => a.pos - b.pos);

    let collisions = 0;
    for (let i = 1; i < points.length; i++) {
      if (points[i].pos === points[i - 1].pos) collisions++;
    }

    this.positions = points.map((p) => p.pos);
    this.owners = points.map((p) => p.owner);
    this.stats = {
      nodes: nodes.length,
      vnodesPerNode: vnodes,
      totalPoints: points.length,
      distinctPositions: points.length - collisions,
      collisions,
    };
  }

  /** 按键查找顺时针方向的第一个虚拟节点所属物理节点 */
  getNode(key: Key): string {
    return this.getNodeByPosition(this.hashFn(key));
  }

  /** 按已计算的环上位置查找（供位宽截断扫描复用预计算哈希值） */
  getNodeByPosition(pos: number): string {
    let lo = 0;
    let hi = this.positions.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.positions[mid] < pos) lo = mid + 1;
      else hi = mid;
    }
    if (lo === this.positions.length) lo = 0; // 环绕
    return this.owners[lo];
  }

  /** 各物理节点拥有的弧长占比（环结构决定的理论负载分布） */
  arcFractions(): Map<string, number> {
    const arcs = new Map<string, number>();
    const n = this.positions.length;
    for (let i = 0; i < n; i++) {
      const prev = i === 0 ? this.positions[n - 1] - this.space : this.positions[i - 1];
      const arc = this.positions[i] - prev;
      const owner = this.owners[i];
      arcs.set(owner, (arcs.get(owner) ?? 0) + arc);
    }
    for (const [k, v] of arcs) arcs.set(k, v / this.space);
    return arcs;
  }
}
