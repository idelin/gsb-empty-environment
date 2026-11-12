export class TicketError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TicketError';
  }
}

export class TicketRevokedError extends TicketError {
  constructor(message = 'ticket has been revoked') {
    super(message);
    this.name = 'TicketRevokedError';
  }
}

export class TicketConsumedError extends TicketError {
  constructor(message = 'ticket has already been consumed') {
    super(message);
    this.name = 'TicketConsumedError';
  }
}

export class UntrustedEventError extends TicketError {
  constructor(message = 'event was not produced by a trusted file picker') {
    super(message);
    this.name = 'UntrustedEventError';
  }
}

export class SessionDisposedError extends TicketError {
  constructor(message = 'selection session has been disposed') {
    super(message);
    this.name = 'SessionDisposedError';
  }
}

export class PathAccessError extends TicketError {
  constructor(message = 'path strings are not file capabilities; a ticket is required') {
    super(message);
    this.name = 'PathAccessError';
  }
}
