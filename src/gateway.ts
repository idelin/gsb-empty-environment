import { KeyRange } from "./key-range.ts";
import type { Lease } from "./lease.ts";
import { LeaseManager } from "./lease.ts";
import { StorageNode } from "./storage-node.ts";
import type { NodeRequest, NodeResponse } from "./storage-node.ts";

export type PartitionState = "active" | "split" | "retired";

export interface Partition {
  readonly partitionId: string;
  readonly range: KeyRange;
  readonly parentId: string | null;
  state: PartitionState;
  dataSize: number;
  lease: Lease | null;
}

export interface SplitResult {
  left: Partition;
  right: Partition;
  leftLease: Lease;
  rightLease: Lease;
}

/**
 * 细粒度分区范围租约路由网关。
 *
 * 职责：
 *  - 维护按 Key Range 排序的分区表，将客户端请求路由到租约持有节点；
 *  - 通过 LeaseManager 授予 / 吊销租约，epoch 作为 fencing token；
 *  - 分区数据过大时触发分裂（Split），子分区继承父租约谱系并各自获得独立租约。
 */
export class LeaseRouterGateway {
  private readonly leaseManager = new LeaseManager();
  private readonly partitions = new Map<string, Partition>();
  private readonly nodes = new Map<string, StorageNode>();
  /** 按 range.start 升序的分区索引（仅含 active 分区） */
  private sortedActive: Partition[] = [];
  private partitionSeq = 0;
  private assignCursor = 0;
  private readonly options: {
    leaseTtlMs?: number;
    maxPartitionSize?: number;
  };

  constructor(
    options: {
      leaseTtlMs?: number;
      maxPartitionSize?: number;
    } = {},
  ) {
    this.options = options;
  }

  registerNode(node: StorageNode): void {
    this.nodes.set(node.nodeId, node);
  }

  /** 创建初始分区（如 [A-Z)）并向指定节点授予租约 */
  bootstrap(range: KeyRange, holderId: string): Partition {
    const partition = this.createPartition(range, null);
    partition.lease = this.leaseManager.grant(
      partition.partitionId,
      holderId,
      this.leaseTtl(),
    );
    this.rebuildIndex();
    return partition;
  }

  /**
   * 在 splitKey 处分裂分区：
   *  1. 吊销父分区租约（fencing：旧持有者立即失效）；
   *  2. 生成左右子分区，继承父分区谱系；
   *  3. 分别为两个子分区授予独立租约（默认轮询分配节点，可显式指定）。
   */
  split(
    partitionId: string,
    splitKey: string,
    holders?: { left: string; right: string },
  ): SplitResult {
    const parent = this.mustGet(partitionId);
    if (parent.state !== "active") {
      throw new Error(`分区 ${partitionId} 状态为 ${parent.state}，不可分裂`);
    }
    if (!parent.range.canSplitAt(splitKey)) {
      throw new Error(
        `分裂点 '${splitKey}' 不在分区 ${parent.range} 内部`,
      );
    }
    const parentLease = parent.lease;
    if (parentLease) this.leaseManager.revoke(parentLease);
    parent.state = "split";

    const left = this.createPartition(
      new KeyRange(parent.range.start, splitKey),
      parent.partitionId,
    );
    const right = this.createPartition(
      new KeyRange(splitKey, parent.range.end),
      parent.partitionId,
    );

    // 租约继承：子租约记录 parentEpoch，形成谱系链
    const leftHolder = holders?.left ?? this.pickNode();
    const rightHolder = holders?.right ?? this.pickNode(leftHolder);
    left.lease = this.leaseManager.grant(
      left.partitionId,
      leftHolder,
      this.leaseTtl(),
      parentLease,
    );
    right.lease = this.leaseManager.grant(
      right.partitionId,
      rightHolder,
      this.leaseTtl(),
      parentLease,
    );

    this.rebuildIndex();
    return { left, right, leftLease: left.lease, rightLease: right.lease };
  }

  /** 上报分区数据量；超过阈值时自动在 splitKey 处分裂 */
  reportSize(partitionId: string, dataSize: number, splitKey?: string): SplitResult | null {
    const partition = this.mustGet(partitionId);
    partition.dataSize = dataSize;
    const threshold = this.options.maxPartitionSize ?? Infinity;
    if (partition.state === "active" && dataSize > threshold) {
      const key = splitKey ?? this.midpointOf(partition.range);
      return this.split(partitionId, key);
    }
    return null;
  }

  /** 路由：key -> active 分区 -> 有效租约的持有节点 */
  route(key: string): { partition: Partition; lease: Lease; node: StorageNode } {
    const partition = this.findPartition(key);
    if (!partition) throw new Error(`没有覆盖 key '${key}' 的分区`);
    if (!partition.lease || !this.leaseManager.isValid(partition.lease)) {
      throw new Error(`分区 ${partition.range} 没有有效租约`);
    }
    const node = this.nodes.get(partition.lease.holderId);
    if (!node) throw new Error(`租约持有者 ${partition.lease.holderId} 未注册`);
    return { partition, lease: partition.lease, node };
  }

  /** 客户端请求入口：路由并转发到租约持有节点 */
  async handleRequest(req: NodeRequest): Promise<NodeResponse> {
    const { node } = this.route(req.key);
    return node.handle(req);
  }

  getPartition(partitionId: string): Partition | undefined {
    return this.partitions.get(partitionId);
  }

  listActivePartitions(): Partition[] {
    return [...this.sortedActive];
  }

  private findPartition(key: string): Partition | null {
    // 二分查找：range.start 有序，定位最后一个 start <= key 的分区
    let lo = 0;
    let hi = this.sortedActive.length - 1;
    let candidate = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      if (this.sortedActive[mid].range.start <= key) {
        candidate = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    if (candidate < 0) return null;
    const partition = this.sortedActive[candidate];
    return partition.range.contains(key) ? partition : null;
  }

  private createPartition(range: KeyRange, parentId: string | null): Partition {
    const partition: Partition = {
      partitionId: `p-${++this.partitionSeq}`,
      range,
      parentId,
      state: "active",
      dataSize: 0,
      lease: null,
    };
    this.partitions.set(partition.partitionId, partition);
    return partition;
  }

  private rebuildIndex(): void {
    this.sortedActive = [...this.partitions.values()]
      .filter((p) => p.state === "active")
      .sort((a, b) => (a.range.start < b.range.start ? -1 : 1));
  }

  private pickNode(exclude?: string): string {
    const ids = [...this.nodes.keys()].filter((id) => id !== exclude);
    if (ids.length === 0) throw new Error("没有可用的存储节点");
    return ids[this.assignCursor++ % ids.length];
  }

  private midpointOf(range: KeyRange): string {
    if (range.end === null) throw new Error("开放区间需要显式指定分裂点");
    const code = (s: string) => s.charCodeAt(0);
    const mid = String.fromCharCode(
      Math.floor((code(range.start) + code(range.end)) / 2),
    );
    if (!range.canSplitAt(mid)) throw new Error("无法推导有效分裂点");
    return mid;
  }

  private mustGet(partitionId: string): Partition {
    const partition = this.partitions.get(partitionId);
    if (!partition) throw new Error(`分区 ${partitionId} 不存在`);
    return partition;
  }

  private leaseTtl(): number {
    return this.options.leaseTtlMs ?? 30_000;
  }
}
