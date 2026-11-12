/**
 * Thrown when a received line violates the wire protocol (illegal control
 * characters, malformed tags, command from an unregistered session, ...).
 *
 * A ProtocolError is fatal to the owning connection: the server emits ERROR
 * and closes the socket rather than trying to recover the stream.
 */
export class ProtocolError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ProtocolError";
  }
}
