import { TicketConsumedError, TicketRevokedError } from './errors.ts';
import type {
  FileObjectDescriptor,
  ReadonlyFileTicket,
  StoredFileObject,
} from './types.ts';

interface TicketRecord {
  token: string;
  documentId: string;
  objectId: string;
  descriptor: FileObjectDescriptor;
  issuedAt: number;
  consumed: boolean;
  revoked: boolean;
  activeStreams: Set<AbortController>;
  view: ReadonlyFileTicket;
}

function randomToken(): string {
  const data = new Uint8Array(32);
  globalThis.crypto.getRandomValues(data);
  let hex = '';
  for (const byte of data) {
    hex += byte.toString(16).padStart(2, '0');
  }
  return `ft_${hex}`;
}

function toTicketView(record: TicketRecord): ReadonlyFileTicket {
  return Object.freeze({
    token: record.token,
    documentId: record.documentId,
    objectId: record.objectId,
    descriptor: Object.freeze({ ...record.descriptor }),
    issuedAt: record.issuedAt,
    revoked: false,
  });
}

/**
 * Authoritative store of minted tickets. All authority (binding, validity,
 * revocation) lives here; ticket objects handed to scripts are inert views.
 */
export class TicketRegistry {
  readonly #records = new Map<string, TicketRecord>();

  issue(documentId: string, file: StoredFileObject, now: number): ReadonlyFileTicket {
    const token = randomToken();
    const record: TicketRecord = {
      token,
      documentId,
      objectId: file.descriptor.objectId,
      descriptor: file.descriptor,
      issuedAt: now,
      consumed: false,
      revoked: false,
      activeStreams: new Set(),
      view: null as unknown as ReadonlyFileTicket,
    };
    record.view = toTicketView(record);
    this.#records.set(token, record);
    return record.view;
  }

  /** Inert view previously handed to a script, or throw for unknown tokens. */
  view(token: string): ReadonlyFileTicket {
    const record = this.#records.get(token);
    if (!record) {
      throw new TicketRevokedError();
    }
    return record.view;
  }

  /**
   * Resolve a token to its live record. Any malformed, forged, consumed or
   * revoked token is rejected. A path or object id cannot be passed here.
   */
  #resolve(token: string): TicketRecord {
    const record = this.#records.get(token);
    if (!record || record.revoked) {
      throw new TicketRevokedError();
    }
    if (record.consumed) {
      throw new TicketConsumedError();
    }
    return record;
  }

  /** One-time redemption: the ticket grants exactly one readable stream. */
  redeem(
    token: string,
    expectedDocumentId: string,
  ): { record: TicketRecord; controller: AbortController } {
    const record = this.#resolve(token);
    if (record.documentId !== expectedDocumentId) {
      throw new TicketRevokedError('ticket is not bound to this document');
    }
    record.consumed = true;
    const controller = new AbortController();
    record.activeStreams.add(controller);
    controller.signal.addEventListener('abort', () => {
      record.activeStreams.delete(controller);
    });
    return { record, controller };
  }

  /**
   * Non-consuming validation used before touching backing storage. Throws
   * exactly like redeem for unknown, revoked, consumed or foreign tokens.
   */
  peek(token: string, expectedDocumentId: string): TicketRecord {
    const record = this.#resolve(token);
    if (record.documentId !== expectedDocumentId) {
      throw new TicketRevokedError('ticket is not bound to this document');
    }
    return record;
  }

  revoke(token: string): void {
    const record = this.#records.get(token);
    if (!record || record.revoked) {
      return;
    }
    record.revoked = true;
    record.view = Object.freeze({ ...record.view, revoked: true });
    this.#abortStreams(record, new TicketRevokedError());
  }

  /** Revoke every ticket, optionally scoped to one document. */
  revokeAll(documentId?: string): number {
    let count = 0;
    const reason = new TicketRevokedError();
    for (const record of this.#records.values()) {
      if (record.revoked) {
        continue;
      }
      if (documentId !== undefined && record.documentId !== documentId) {
        continue;
      }
      record.revoked = true;
      record.view = Object.freeze({ ...record.view, revoked: true });
      this.#abortStreams(record, reason);
      count += 1;
    }
    return count;
  }

  isLive(token: string): boolean {
    const record = this.#records.get(token);
    return !!record && !record.revoked && !record.consumed;
  }

  #abortStreams(record: TicketRecord, reason: TicketRevokedError): void {
    for (const controller of record.activeStreams) {
      if (!controller.signal.aborted) {
        controller.abort(reason);
      }
    }
    record.activeStreams.clear();
  }
}
