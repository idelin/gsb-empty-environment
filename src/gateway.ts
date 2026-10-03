/**
 * 双层层次化哈希聚合路由网关。
 *
 * 第一层（集群层）：按 Tenant ID 哈希，路由到集群网关节点 —— 全局共享一个红黑树哈希环。
 * 第二层（租户层）：按设备 Mac 地址哈希，路由到该租户的协议处理节点 ——
 *   每个租户独占一个红黑树哈希环，因此单租户扩缩容只扰动本租户的映射，
 *   其他租户的哈希分布完全不受影响。
 *
 * 两层环相互独立，各自支持 add/remove 节点实现扩缩容与容灾摘除。
 */
import { HashRing, fnv1a32 } from "./hashRing.ts";

export interface RouteHop {
  layer: "cluster" | "tenant";
  ringKey: string;
  nodeId: string;
}

export interface RouteResult {
  tenantId: string;
  deviceMac: string;
  /** 第一层命中的集群网关节点 */
  clusterGateway: string;
  /** 第二层命中的协议处理节点 */
  protocolNode: string;
  /** 级联穿透路径（两跳） */
  path: RouteHop[];
}

export class HierarchicalGateway {
  private readonly clusterRing = new HashRing();
  private readonly tenantRings = new Map<string, HashRing>();
  private readonly vnodeCount: number;

  constructor(vnodeCount = 160) {
    this.vnodeCount = vnodeCount;
  }

  // ---------- 第一层：集群网关环 ----------

  addClusterGateway(gatewayId: string): void {
    this.clusterRing.addNode(gatewayId);
  }

  removeClusterGateway(gatewayId: string): boolean {
    return this.clusterRing.removeNode(gatewayId);
  }

  get clusterGateways(): string[] {
    return this.clusterRing.nodes;
  }

  // ---------- 第二层：租户协议节点环 ----------

  private ensureTenantRing(tenantId: string): HashRing {
    let ring = this.tenantRings.get(tenantId);
    if (ring === undefined) {
      ring = new HashRing(this.vnodeCount);
      this.tenantRings.set(tenantId, ring);
    }
    return ring;
  }

  addProtocolNode(tenantId: string, nodeId: string): void {
    this.ensureTenantRing(tenantId).addNode(nodeId);
  }

  removeProtocolNode(tenantId: string, nodeId: string): boolean {
    const ring = this.tenantRings.get(tenantId);
    if (ring === undefined) return false;
    return ring.removeNode(nodeId);
  }

  protocolNodesOf(tenantId: string): string[] {
    return this.tenantRings.get(tenantId)?.nodes ?? [];
  }

  // ---------- 级联路由 ----------

  /** 第一层：Tenant ID -> 集群网关节点 */
  routeCluster(tenantId: string): string {
    return this.clusterRing.route(`tenant:${tenantId}`);
  }

  /** 第二层：设备 Mac -> 该租户的协议处理节点 */
  routeProtocolNode(tenantId: string, deviceMac: string): string {
    const ring = this.tenantRings.get(tenantId);
    if (ring === undefined || ring.nodeCount === 0) {
      throw new Error(`tenant has no protocol nodes: ${tenantId}`);
    }
    return ring.route(`device:${deviceMac}`);
  }

  /** 双层级联穿透：Tenant -> 集群网关 -> 租户环 -> 协议节点 */
  route(tenantId: string, deviceMac: string): RouteResult {
    const clusterGateway = this.routeCluster(tenantId);
    const protocolNode = this.routeProtocolNode(tenantId, deviceMac);
    return {
      tenantId,
      deviceMac,
      clusterGateway,
      protocolNode,
      path: [
        { layer: "cluster", ringKey: `tenant:${tenantId}`, nodeId: clusterGateway },
        { layer: "tenant", ringKey: `device:${deviceMac}`, nodeId: protocolNode },
      ],
    };
  }

  /** 调试辅助：暴露 key 在环上的落点哈希。 */
  debugHash(layer: "cluster" | "tenant", key: string): number {
    return fnv1a32(layer === "cluster" ? `tenant:${key}` : `device:${key}`);
  }
}
