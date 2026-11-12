/**
 * Error codes emitted by the blob address registry.
 *
 * Every failed resolution is reported as an {@link AddressError}. Callers
 * should branch on `code` rather than string matching on `message`.
 */
export type AddressErrorCode =
  | "ADDRESS_DENIED"
  | "ADDRESS_NOT_FOUND"
  | "ADDRESS_REVOKED"
  | "READ_ABORTED"
  | "READ_CLOSED";

export class AddressError extends Error {
  readonly code: AddressErrorCode;

  constructor(code: AddressErrorCode, message: string) {
    super(message);
    this.name = "AddressError";
    this.code = code;
  }
}

/**
 * The calling document is not allowed to observe this address. Used for
 * inactive documents, origin/partition/generation mismatches and malformed
 * blob URLs, so that unauthorised callers cannot distinguish "missing",
 * "revoked" and "belongs to another tenant" from each other.
 */
export class AddressDeniedError extends AddressError {
  constructor(message = "calling document is not authorised for this address") {
    super("ADDRESS_DENIED", message);
    this.name = "AddressDeniedError";
  }
}

export class AddressNotFoundError extends AddressError {
  constructor(message = "blob address does not exist") {
    super("ADDRESS_NOT_FOUND", message);
    this.name = "AddressNotFoundError";
  }
}

export class AddressRevokedError extends AddressError {
  constructor(message = "blob address has been revoked") {
    super("ADDRESS_REVOKED", message);
    this.name = "AddressRevokedError";
  }
}

export class ReadAbortedError extends AddressError {
  constructor(message = "blob read was aborted before the stream was issued") {
    super("READ_ABORTED", message);
    this.name = "ReadAbortedError";
  }
}

export class ReadClosedError extends AddressError {
  constructor(message = "blob read stream is already closed") {
    super("READ_CLOSED", message);
    this.name = "ReadClosedError";
  }
}
