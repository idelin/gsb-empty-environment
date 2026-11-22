import type { FileObjectStore } from "./store.ts";
import { claimTrustedEvent } from "./trust.ts";
import type { TrustedPickerEvent } from "./trust.ts";
import {
  ForeignTicketError,
  TicketRevokedError,
  UnknownObjectError,
  UnknownTicketError,
} from "./errors.ts";

const CHUNK_SIZE = 16 * 1024;
const TICKET_BRAND = Symbol("selection-session.ticket");

interface TicketRecord {
  readonly session: SelectionSession;
  readonly objectId: string;
  readonly generation: number;
}

interface TicketInit {
  readonly objectId: string;
  readonly documentId: string;
  readonly name: string;
  readonly size: number;
}

const ticketRecords = new WeakMap<FileTicket, TicketRecord>();

/**
 * Read-only capability to one file object, bound to a document id and an
 * object id. Scripts may hold and inspect tickets, but cannot mint them:
 * the constructor requires a module-private brand, and the internal record
 * lives in a module-private WeakMap. A ticket is only useful with the
 * SelectionSession that issued it, and only while its generation is current.
 */
export class FileTicket {
  readonly objectId: string;
  readonly documentId: string;
  readonly name: string;
  readonly size: number;

  constructor(brand: symbol, init: TicketInit) {
    if (brand !== TICKET_BRAND) {
      throw new UnknownTicketError(
        "FileTicket instances are minted only by a SelectionSession",
      );
    }
    this.objectId = init.objectId;
    this.documentId = init.documentId;
    this.name = init.name;
    this.size = init.size;
    Object.freeze(this);
  }
}

export interface SelectionSessionInit {
  readonly documentId: string;
  readonly store: FileObjectStore;
}

/**
 * Per-document selection session. Applies trusted picker events to replace
 * the readable set, mints tickets for the current selection, and opens read
 * streams for valid tickets. Re-selection, cancellation, and navigation
 * revoke every previously issued ticket.
 */
export class SelectionSession {
  #store: FileObjectStore;
  #documentId: string;
  #generation = 0;
  #tickets = new Set<FileTicket>();

  constructor(init: SelectionSessionInit) {
    this.#store = init.store;
    this.#documentId = init.documentId;
  }

  get documentId(): string {
    return this.#documentId;
  }

  get size(): number {
    return this.#tickets.size;
  }

  currentTickets(): readonly FileTicket[] {
    return Object.freeze([...this.#tickets]);
  }

  /**
   * Apply a trusted picker event. A "selection" event atomically replaces the
   * readable set (old tickets are revoked before new ones are minted); a
   * "cancel" event empties it. Returns the live tickets for the new set.
   */
  applySelection(event: TrustedPickerEvent): FileTicket[] {
    const claimed = claimTrustedEvent(event);
    if (claimed.kind === "cancel") {
      this.#revokeAll();
      return [];
    }
    const metas = claimed.objectIds.map((objectId) => {
      const meta = this.#store.describe(objectId);
      if (!meta) {
        throw new UnknownObjectError(
          `selection references unknown object id: ${objectId}`,
        );
      }
      return meta;
    });
    this.#revokeAll();
    const tickets = metas.map((meta) => {
      const ticket = new FileTicket(TICKET_BRAND, {
        objectId: meta.objectId,
        documentId: this.#documentId,
        name: meta.name,
        size: meta.size,
      });
      ticketRecords.set(ticket, {
        session: this,
        objectId: meta.objectId,
        generation: this.#generation,
      });
      this.#tickets.add(ticket);
      return ticket;
    });
    return Object.freeze(tickets);
  }

  /**
   * Document navigation: the document identity changes and every outstanding
   * ticket is revoked. Nothing carries over across a navigation.
   */
  navigateTo(documentId: string): void {
    this.#documentId = documentId;
    this.#revokeAll();
  }

  /**
   * Open a read-only byte stream for a ticket. The ticket must have been
   * minted by this session and still be live. Bytes are snapshotted at open
   * time, so a stream already in flight finishes with the file it started
   * with and never observes a later selection.
   */
  openReadStream(ticket: unknown): ReadableStream<Uint8Array> {
    const record = this.#authorize(ticket);
    const bytes = this.#store.snapshot(record.objectId);
    if (!bytes) {
      throw new UnknownObjectError(
        `backing object is gone for id: ${record.objectId}`,
      );
    }
    let offset = 0;
    return new ReadableStream<Uint8Array>({
      pull: (controller) => {
        if (offset >= bytes.length) {
          controller.close();
          return;
        }
        const end = Math.min(offset + CHUNK_SIZE, bytes.length);
        controller.enqueue(bytes.subarray(offset, end));
        offset = end;
      },
    });
  }

  #authorize(ticket: unknown): TicketRecord {
    if (
      ticket === null ||
      (typeof ticket !== "object" && typeof ticket !== "function")
    ) {
      throw new UnknownTicketError(
        "not a file ticket: path strings and plain data are not capabilities",
      );
    }
    const record = ticketRecords.get(ticket as FileTicket);
    if (!record) {
      throw new UnknownTicketError(
        "not a file ticket minted by any SelectionSession",
      );
    }
    if (record.session !== this) {
      throw new ForeignTicketError(
        "ticket belongs to a different selection session",
      );
    }
    if (record.generation !== this.#generation) {
      throw new TicketRevokedError(
        "ticket was revoked by re-selection, cancellation, or navigation",
      );
    }
    return record;
  }

  #revokeAll(): void {
    this.#generation += 1;
    this.#tickets.clear();
  }
}
