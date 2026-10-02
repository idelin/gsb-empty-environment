export class NoReadyNodeError extends Error {
  constructor(message = "no ready node available in route pool") {
    super(message);
    this.name = "NoReadyNodeError";
  }
}

export class NodeDrainingError extends Error {
  constructor(nodeId: string) {
    super(`node "${nodeId}" is Leaving and rejects new traffic`);
    this.name = "NodeDrainingError";
  }
}

export class UnknownNodeError extends Error {
  constructor(nodeId: string) {
    super(`node "${nodeId}" is not present in the route table`);
    this.name = "UnknownNodeError";
  }
}
