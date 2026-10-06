// Virtual object graph: a controlled, in-memory stand-in for a debuggee heap.
// Nodes have stable identity, may be shared by many parents, and may form
// cycles. Accessor properties count their invocations so tests can prove
// that getters are not executed unless explicitly requested.

export type VPrimitive = string | number | boolean | null | undefined;
export type VValue = VNode | VPrimitive;

export interface VDataProp {
  kind: 'data';
  value: VValue;
}

export interface VAccessorProp {
  kind: 'accessor';
  get: () => VValue;
}

export type VProp = VDataProp | VAccessorProp;

let nextNodeId = 1;

export class VNode {
  readonly nodeId: number;
  readonly className: string;
  readonly props = new Map<string, VProp>();

  constructor(className: string = 'Object') {
    this.nodeId = nextNodeId++;
    this.className = className;
  }

  set(name: string, value: VValue): this {
    this.props.set(name, { kind: 'data', value });
    return this;
  }

  accessor(name: string, get: () => VValue): this {
    this.props.set(name, { kind: 'accessor', get });
    return this;
  }
}

export class VirtualGraph {
  private getterInvocations = 0;

  get getterCalls(): number {
    return this.getterInvocations;
  }

  node(className?: string): VNode {
    return new VNode(className);
  }

  // Defines an accessor property whose invocations are counted by the graph.
  trackedAccessor(node: VNode, name: string, compute: () => VValue): VNode {
    return node.accessor(name, () => {
      this.getterInvocations += 1;
      return compute();
    });
  }
}
