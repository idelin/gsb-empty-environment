/**
 * Base class for every protocol-level failure raised by this package.
 */
export class IrcProtocolError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'IrcProtocolError';
  }
}

/**
 * Raised when a received line violates the wire format rules, for example
 * because it contains a bare CR, LF or NUL byte. Such lines must never be
 * parsed into a command and the connection should be closed.
 */
export class ProtocolError extends IrcProtocolError {
  constructor(message: string) {
    super(message);
    this.name = 'ProtocolError';
  }
}

/**
 * Raised when an outgoing message cannot be serialized safely. Serialization
 * is fail-closed: data that could smuggle tags, parameters or commands is
 * rejected instead of written to the wire.
 */
export class SerializationError extends IrcProtocolError {
  constructor(message: string) {
    super(message);
    this.name = 'SerializationError';
  }
}
