export interface TopoNode {
  id: string;
  region: string;
  zone: string;
}

export interface ClientLoc {
  region: string;
  zone: string;
}

export const DIST_SAME_ZONE = 1;
export const DIST_SAME_REGION = 2;
export const DIST_CROSS_REGION = 3;

/**
 * Two-level hierarchy (region -> zone -> node) with hierarchical distance
 * used by the locality-aware router.
 */
export class Topology {
  private readonly nodes = new Map<string, TopoNode>();

  addNode(node: TopoNode): void {
    this.nodes.set(node.id, node);
  }

  removeNode(id: string): void {
    this.nodes.delete(id);
  }

  get(id: string): TopoNode | undefined {
    return this.nodes.get(id);
  }

  get size(): number {
    return this.nodes.size;
  }

  distanceToNode(client: ClientLoc, node: TopoNode): number {
    if (client.region === node.region && client.zone === node.zone) {
      return DIST_SAME_ZONE;
    }
    if (client.region === node.region) return DIST_SAME_REGION;
    return DIST_CROSS_REGION;
  }

  distance(client: ClientLoc, nodeId: string): number {
    const node = this.nodes.get(nodeId);
    if (!node) return Number.POSITIVE_INFINITY;
    return this.distanceToNode(client, node);
  }
}
