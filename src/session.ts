import { SessionDisposedError, TicketRevokedError } from './errors.ts';
import { TicketRegistry } from './registry.ts';
import type { FileStorage } from './storage.ts';
import { assertTrusted } from './trustedEvents.ts';
import type {
  DocumentNavigateEvent,
  ReadonlyFileTicket,
  SelectionCancelEvent,
  SelectionChangeEvent,
  SessionDisposeEvent,
  SessionEvent,
  SessionEventMap,
  SessionListener,
  SessionEventType,
} from './types.ts';

type GenericListener = (event: SessionEvent) => void;

/**
 * A selection session is bound to exactly one document. It mints tickets
 * only for trusted picker events, replaces the whole ticket set on every
 * new selection, and tears down all capabilities on navigation/disposal.
 */
export class SelectionSession {
  #documentId: string;
  readonly #registry: TicketRegistry;
  readonly #storage: FileStorage;
  readonly #listeners = new Map<SessionEventType, Set<GenericListener>>();
  #selectionId = 0;
  #currentTokens = new Set<string>();
  #navigatedDocumentId: string | null = null;
  #disposed = false;

  constructor(documentId: string, storage: FileStorage, registry?: TicketRegistry) {
    this.#documentId = documentId;
    this.#storage = storage;
    this.#registry = registry ?? new TicketRegistry();
  }

  get documentId(): string {
    return this.#documentId;
  }

  get selectionId(): number {
    return this.#selectionId;
  }

  get disposed(): boolean {
    return this.#disposed;
  }

  /** Tickets that belong to the current (latest) selection. */
  get currentTickets(): readonly ReadonlyFileTicket[] {
    return [...this.#currentTokens]
      .filter((token) => this.#registry.isLive(token))
      .map((token) => this.#registry.view(token));
  }

  addEventListener<K extends SessionEventType>(
    type: K,
    listener: SessionListener<SessionEventMap[K]>,
  ): void {
    let set = this.#listeners.get(type);
    if (!set) {
      set = new Set();
      this.#listeners.set(type, set);
    }
    set.add(listener as GenericListener);
  }

  removeEventListener<K extends SessionEventType>(
    type: K,
    listener: SessionListener<SessionEventMap[K]>,
  ): void {
    this.#listeners.get(type)?.delete(listener as GenericListener);
  }

  #emit(event: SessionEvent): void {
    const set = this.#listeners.get(event.type);
    if (!set) {
      return;
    }
    for (const listener of [...set]) {
      listener(event);
    }
  }

  /**
   * Trusted entry point used by the picker host. Forged or synthetic events
   * lack the internal brand and are rejected before any ticket is minted.
   */
  handleTrustedPickerEvent(event: object): void {
    if (this.#disposed) {
      throw new SessionDisposedError();
    }
    if (this.#navigatedDocumentId !== null) {
      throw new SessionDisposedError('document has navigated away');
    }
    assertTrusted(event);

    if (event.kind === 'cancel') {
      const cancelEvent: SelectionCancelEvent = Object.freeze({
        type: 'cancel',
        isTrusted: true,
        selectionId: event.selectionId,
      });
      this.#emit(cancelEvent);
      return;
    }

    this.#selectionId = event.selectionId;
    const previousTokens = this.#currentTokens;
    const now = Date.now();
    const tickets: ReadonlyFileTicket[] = event.files.map((file) =>
      this.#registry.issue(this.#documentId, file, now),
    );
    this.#currentTokens = new Set(tickets.map((ticket) => ticket.token));

    for (const token of previousTokens) {
      this.#registry.revoke(token);
    }

    const selectionEvent: SelectionChangeEvent = Object.freeze({
      type: 'selection',
      isTrusted: true,
      selectionId: event.selectionId,
      tickets: Object.freeze([...tickets]),
    });
    this.#emit(selectionEvent);
  }

  /**
   * The only way page scripts read file bytes. A path string has no entry
   * point anywhere: callers must present a ticket previously handed to them.
   * Tickets are single-use and bound to this session's document.
   */
  openReadStream(ticket: ReadonlyFileTicket): ReadableStream<Uint8Array> {
    if (this.#disposed) {
      throw new SessionDisposedError();
    }
    if (this.#navigatedDocumentId !== null) {
      throw new TicketRevokedError('document has navigated away');
    }

    const preview = this.#registry.peek(ticket.token, this.#documentId);
    const file = this.#storage.get(preview.objectId);
    if (!file) {
      this.#registry.revoke(ticket.token);
      throw new TicketRevokedError('backing file object is no longer available');
    }
    const { controller } = this.#registry.redeem(ticket.token, this.#documentId);

    const bytes = file.bytes;
    let offset = 0;
    const signal = controller.signal;

    return new ReadableStream<Uint8Array>({
      start(controller2): void {
        signal.addEventListener('abort', () => {
          controller2.error(signal.reason ?? new TicketRevokedError());
        });
      },
      pull(controller2): void {
        if (signal.aborted) {
          controller2.error(signal.reason ?? new TicketRevokedError());
          return;
        }
        if (offset >= bytes.length) {
          controller2.close();
          return;
        }
        const end = Math.min(offset + 64 * 1024, bytes.length);
        controller2.enqueue(bytes.slice(offset, end));
        offset = end;
      },
      cancel(): void {
        if (!signal.aborted) {
          controller.abort(new Error('stream cancelled by reader'));
        }
      },
    });
  }

  /**
   * Document navigation revokes every ticket for the old document and ends
   * any in-flight read streams. The session can no longer serve reads.
   */
  navigate(toDocumentId: string): DocumentNavigateEvent {
    if (this.#disposed) {
      throw new SessionDisposedError();
    }
    if (this.#navigatedDocumentId !== null) {
      throw new SessionDisposedError('document has already navigated');
    }
    const fromDocumentId = this.#documentId;
    this.#navigatedDocumentId = toDocumentId;
    this.#registry.revokeAll(fromDocumentId);
    this.#currentTokens.clear();

    const event: DocumentNavigateEvent = Object.freeze({
      type: 'navigate',
      isTrusted: true,
      fromDocumentId,
      toDocumentId,
    });
    this.#emit(event);
    return event;
  }

  dispose(): void {
    if (this.#disposed) {
      return;
    }
    this.#disposed = true;
    this.#registry.revokeAll(this.#documentId);
    this.#currentTokens.clear();

    const event: SessionDisposeEvent = Object.freeze({
      type: 'dispose',
      isTrusted: true,
      documentId: this.#documentId,
    });
    this.#emit(event);
    this.#listeners.clear();
  }
}
