import { HandshakeErrorCode, HandshakeRejected } from './errors.ts';
import { originMatches, parseOrigin } from './origin.ts';

export interface HandshakeTicket {
  nonce: string;
  origin: string;
  scope: string;
  expiresAt: number;
}

export interface TicketStore {
  consume(nonce: string, now: number): HandshakeTicket | null;
}

export interface TicketIssuerOptions {
  ttlMs?: number;
}

function randomNonce(): string {
  const bytes = new Uint8Array(18);
  globalThis.crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

export class HandshakeTicketIssuer {
  private readonly ttlMs: number;

  constructor(options: TicketIssuerOptions = {}) {
    this.ttlMs = options.ttlMs ?? 60_000;
  }

  issue(origin: string, scope: string, now: number = Date.now()): HandshakeTicket {
    parseOrigin(origin);
    if (typeof scope !== 'string' || scope.length === 0) {
      throw new Error('ticket scope must be a non-empty string');
    }
    return {
      nonce: randomNonce(),
      origin,
      scope,
      expiresAt: now + this.ttlMs,
    };
  }
}

export class InMemoryTicketStore implements TicketStore {
  private readonly tickets = new Map<string, HandshakeTicket>();

  add(ticket: HandshakeTicket): void {
    this.tickets.set(ticket.nonce, { ...ticket });
  }

  consume(nonce: string, now: number): HandshakeTicket | null {
    const ticket = this.tickets.get(nonce);
    if (!ticket) {
      return null;
    }
    if (ticket.expiresAt <= now) {
      this.tickets.delete(nonce);
      throw new HandshakeRejected(
        HandshakeErrorCode.TICKET_EXPIRED,
        'handshake ticket has expired',
      );
    }
    this.tickets.delete(nonce);
    return { ...ticket };
  }

  get size(): number {
    return this.tickets.size;
  }
}

export function parseTicket(raw: string): HandshakeTicket {
  let payload: unknown;
  try {
    payload = JSON.parse(raw);
  } catch {
    throw new HandshakeRejected(
      HandshakeErrorCode.MALFORMED_TICKET,
      'handshake ticket is not valid JSON',
    );
  }
  const candidate = payload as Partial<HandshakeTicket> | null;
  if (
    !candidate ||
    typeof candidate.nonce !== 'string' ||
    candidate.nonce.length === 0 ||
    typeof candidate.origin !== 'string' ||
    typeof candidate.scope !== 'string' ||
    typeof candidate.expiresAt !== 'number'
  ) {
    throw new HandshakeRejected(
      HandshakeErrorCode.MALFORMED_TICKET,
      'handshake ticket is missing required fields',
    );
  }
  try {
    parseOrigin(candidate.origin);
  } catch {
    throw new HandshakeRejected(
      HandshakeErrorCode.MALFORMED_TICKET,
      'handshake ticket carries a malformed origin',
    );
  }
  return {
    nonce: candidate.nonce,
    origin: candidate.origin,
    scope: candidate.scope,
    expiresAt: candidate.expiresAt,
  };
}

export function ticketMatchesRequest(
  ticket: HandshakeTicket,
  origin: string,
  scope: string,
): HandshakeErrorCode | null {
  if (!originMatches(ticket.origin, origin)) {
    return HandshakeErrorCode.TICKET_ORIGIN_MISMATCH;
  }
  if (ticket.scope !== scope) {
    return HandshakeErrorCode.TICKET_SCOPE_MISMATCH;
  }
  return null;
}
