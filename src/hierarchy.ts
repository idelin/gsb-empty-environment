import type { Policy, ResourceNode, Tags } from './types.ts';

export class ResourceTree {
  private readonly nodes = new Map<string, ResourceNode>();

  constructor(nodes: ResourceNode[] = []) {
    for (const node of nodes) {
      this.add(node);
    }
    this.validate();
  }

  add(node: ResourceNode): void {
    this.nodes.set(node.id, node);
  }

  validate(): void {
    for (const node of this.nodes.values()) {
      if (node.parent !== undefined && !this.nodes.has(node.parent)) {
        throw new Error(`resource ${node.id} references missing parent ${node.parent}`);
      }
    }
    for (const node of this.nodes.values()) {
      const seen = new Set<string>([node.id]);
      let current = node.parent;
      while (current !== undefined) {
        if (seen.has(current)) {
          throw new Error(`cycle detected in resource hierarchy at ${current}`);
        }
        seen.add(current);
        current = this.nodes.get(current)?.parent;
      }
    }
  }

  get(id: string): ResourceNode {
    const node = this.nodes.get(id);
    if (!node) {
      throw new Error(`unknown resource ${id}`);
    }
    return node;
  }

  chain(id: string): ResourceNode[] {
    const ordered: ResourceNode[] = [];
    let current: ResourceNode | undefined = this.get(id);
    while (current) {
      ordered.unshift(current);
      current = current.parent ? this.nodes.get(current.parent) : undefined;
    }
    return ordered;
  }

  effectiveTags(id: string): Tags {
    const merged: Tags = {};
    for (const node of this.chain(id)) {
      Object.assign(merged, node.tags ?? {});
    }
    return merged;
  }

  applicablePolicies(id: string): { policy: Policy; attachedAt: string }[] {
    const result: { policy: Policy; attachedAt: string }[] = [];
    for (const node of this.chain(id)) {
      for (const policy of node.policies ?? []) {
        result.push({ policy, attachedAt: node.id });
      }
    }
    return result;
  }
}
