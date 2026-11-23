export type BridgeErrorCode =
  | 'BRIDGE_VALIDATION_ERROR'
  | 'BRIDGE_UNSAFE_ARGUMENT'
  | 'BRIDGE_UNREGISTERED_CHANNEL'
  | 'BRIDGE_SENDER_REJECTED'
  | 'BRIDGE_DUPLICATE_REGISTRATION'
  | 'BRIDGE_INVALID_DEFINITION';

export class BridgeError extends Error {
  readonly code: BridgeErrorCode | string;

  constructor(message: string, code: BridgeErrorCode | string = 'BRIDGE_ERROR') {
    super(message);
    this.name = new.target.name;
    this.code = code;
  }
}

export class ValidationError extends BridgeError {
  readonly path: string;

  constructor(path: string, message: string) {
    super(`${path}: ${message}`, 'BRIDGE_VALIDATION_ERROR');
    this.path = path;
  }
}

export class UnsafeArgumentError extends BridgeError {
  constructor(message: string) {
    super(message, 'BRIDGE_UNSAFE_ARGUMENT');
  }
}

export class UnregisteredChannelError extends BridgeError {
  readonly channel: string;

  constructor(channel: string) {
    super(`IPC channel is not registered with the bridge: ${channel}`, 'BRIDGE_UNREGISTERED_CHANNEL');
    this.channel = channel;
  }
}

export class SenderRejectedError extends BridgeError {
  constructor(message: string) {
    super(message, 'BRIDGE_SENDER_REJECTED');
  }
}

export class DuplicateRegistrationError extends BridgeError {
  constructor(message: string) {
    super(message, 'BRIDGE_DUPLICATE_REGISTRATION');
  }
}

export class InvalidDefinitionError extends BridgeError {
  constructor(message: string) {
    super(message, 'BRIDGE_INVALID_DEFINITION');
  }
}
