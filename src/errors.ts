export class HostError extends Error {
  readonly code: string;

  constructor(code: string, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "HostError";
    this.code = code;
  }
}

export class UnsafeDescriptionError extends HostError {
  constructor(message: string) {
    super("ERR_UNSAFE_DESCRIPTION", message);
    this.name = "UnsafeDescriptionError";
  }
}

export class InvalidDescriptionError extends HostError {
  constructor(message: string) {
    super("ERR_INVALID_DESCRIPTION", message);
    this.name = "InvalidDescriptionError";
  }
}

export class UnknownNodeError extends HostError {
  readonly nodeId: string;

  constructor(nodeId: string) {
    super("ERR_UNKNOWN_NODE", `Unknown description node: ${nodeId}`);
    this.name = "UnknownNodeError";
    this.nodeId = nodeId;
  }
}

export class PolicyError extends HostError {
  constructor(message: string) {
    super("ERR_POLICY", message);
    this.name = "PolicyError";
  }
}

export class CapabilityError extends HostError {
  readonly capability: string | null;

  constructor(message: string, capability: string | null = null) {
    super("ERR_CAPABILITY", message);
    this.name = "CapabilityError";
    this.capability = capability;
  }
}

export class RevokedViewError extends HostError {
  readonly edgeId: string | null;

  constructor(message = "The proxy view has been revoked", edgeId: string | null = null) {
    super("ERR_VIEW_REVOKED", message);
    this.name = "RevokedViewError";
    this.edgeId = edgeId;
  }
}

export class SymbolicOperationError extends HostError {
  constructor(message: string) {
    super("ERR_SYMBOLIC_OPERATION", message);
    this.name = "SymbolicOperationError";
  }
}

export class InvalidWriteError extends HostError {
  constructor(message: string) {
    super("ERR_INVALID_WRITE", message);
    this.name = "InvalidWriteError";
  }
}
