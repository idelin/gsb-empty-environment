import { HashRing } from './hashRing.ts';

export interface GatewayCluster {
  id: string;
  weight?: number;
}

export interface ProtocolNode {
  id: string;
  weight?: number;
}

export interface RouteRequest {
  tenant: string | number;
  device: string;
}

export interface RouteHop {
  layer: 1 | 2;
  key: string;
  target: string;
}

export interface Route {
  tenant: string;
  device: string;
  cluster: string;
  node: string;
  hops: RouteHop[];
}

export class HierarchicalGateway {
  private readonly clusterRing = new HashRing<GatewayCluster>();
  private readonly tenantRings = new Map<string, HashRing<ProtocolNode>>();
  private readonly downClusters = new Set<string>();
  private readonly downNodes = new Map<string, Set<string>>();

  addCluster(cluster: GatewayCluster): void {
    this.clusterRing.add(cluster);
  }

  removeCluster(id: string): void {
    this.clusterRing.remove(id);
    this.downClusters.delete(id);
  }

  setClusterHealth(id: string, alive: boolean): void {
    if (alive) {
      this.downClusters.delete(id);
    } else {
      this.downClusters.add(id);
    }
  }

  provisionTenant(tenant: string | number, nodes: ProtocolNode[]): void {
    const ring = this.tenantRing(String(tenant));
    for (const node of nodes) {
      ring.add(node);
    }
  }

  addNode(tenant: string | number, node: ProtocolNode): void {
    this.tenantRing(String(tenant)).add(node);
  }

  removeNode(tenant: string | number, nodeId: string): void {
    const key = String(tenant);
    this.tenantRings.get(key)?.remove(nodeId);
    this.downNodes.get(key)?.delete(nodeId);
  }

  setNodeHealth(tenant: string | number, nodeId: string, alive: boolean): void {
    const key = String(tenant);
    let down = this.downNodes.get(key);
    if (alive) {
      down?.delete(nodeId);
      return;
    }
    if (down === undefined) {
      down = new Set<string>();
      this.downNodes.set(key, down);
    }
    down.add(nodeId);
  }

  route(request: RouteRequest): Route {
    const tenant = String(request.tenant);
    const cluster = this.clusterRing.lookup(tenant, this.downClusters);
    if (cluster === null) {
      throw new Error('no alive gateway cluster available');
    }
    const ring = this.tenantRings.get(tenant);
    const node = ring?.lookup(request.device, this.downNodes.get(tenant)) ?? null;
    if (node === null) {
      throw new Error(`no alive protocol node for tenant ${tenant}`);
    }
    return {
      tenant,
      device: request.device,
      cluster,
      node,
      hops: [
        { layer: 1, key: tenant, target: cluster },
        { layer: 2, key: request.device, target: node },
      ],
    };
  }

  clusterOf(tenant: string | number): string | null {
    return this.clusterRing.lookup(String(tenant), this.downClusters);
  }

  nodeOf(tenant: string | number, device: string): string | null {
    const key = String(tenant);
    const ring = this.tenantRings.get(key);
    return ring?.lookup(device, this.downNodes.get(key)) ?? null;
  }

  private tenantRing(tenant: string): HashRing<ProtocolNode> {
    let ring = this.tenantRings.get(tenant);
    if (ring === undefined) {
      ring = new HashRing<ProtocolNode>();
      this.tenantRings.set(tenant, ring);
    }
    return ring;
  }
}
