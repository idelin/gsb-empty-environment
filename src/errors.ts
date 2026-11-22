export class SelectionSessionError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = new.target.name;
    this.code = code;
  }
}

export class UntrustedSelectionError extends SelectionSessionError {
  constructor(message: string) {
    super("ERR_UNTRUSTED_SELECTION", message);
  }
}

export class UnknownTicketError extends SelectionSessionError {
  constructor(message: string) {
    super("ERR_UNKNOWN_TICKET", message);
  }
}

export class ForeignTicketError extends SelectionSessionError {
  constructor(message: string) {
    super("ERR_FOREIGN_TICKET", message);
  }
}

export class TicketRevokedError extends SelectionSessionError {
  constructor(message: string) {
    super("ERR_TICKET_REVOKED", message);
  }
}

export class UnknownObjectError extends SelectionSessionError {
  constructor(message: string) {
    super("ERR_UNKNOWN_OBJECT", message);
  }
}
