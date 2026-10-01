export class NodeDrainingError extends Error {
  readonly code = "ERR_NODE_DRAINING";
  readonly nodeId: string;

  constructor(nodeId: string) {
    super(
      `node "${nodeId}" is leaving: new traffic is blocked during the quiescent period`,
    );
    this.name = "NodeDrainingError";
    this.nodeId = nodeId;
  }
}

export class NodeNotFoundError extends Error {
  readonly code = "ERR_NODE_NOT_FOUND";
  readonly nodeId: string;

  constructor(nodeId: string) {
    super(`node "${nodeId}" is not present in the routing table`);
    this.name = "NodeNotFoundError";
    this.nodeId = nodeId;
  }
}
