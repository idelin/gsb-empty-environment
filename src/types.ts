/**
 * Public type surface for the trusted file-selection session package.
 *
 * Capability model:
 *  - A path string or object id is never sufficient to read file bytes.
 *  - The only readable capability is a ReadonlyFileTicket minted by the
 *    trusted host in response to an explicit user selection.
 *  - Every ticket is bound to a document id and a file object id.
 */

export interface FileObjectDescriptor {
  readonly objectId: string;
  readonly name: string;
  readonly size: number;
  readonly lastModified: number;
}

export interface ReadonlyFileTicket {
  /** Opaque, unforgeable capability token. Guessing it is infeasible. */
  readonly token: string;
  /** Document the ticket was issued for. */
  readonly documentId: string;
  /** File object the ticket is bound to. */
  readonly objectId: string;
  /** Non-sensitive metadata the page is allowed to display. */
  readonly descriptor: FileObjectDescriptor;
  readonly issuedAt: number;
  /** Mirror of registry state; authority always lives in the registry. */
  readonly revoked: boolean;
}

export type SessionEventType = 'selection' | 'cancel' | 'navigate' | 'dispose';

export interface SessionEventMap {
  selection: SelectionChangeEvent;
  cancel: SelectionCancelEvent;
  navigate: DocumentNavigateEvent;
  dispose: SessionDisposeEvent;
}

export interface SessionEventBase {
  readonly type: SessionEventType;
  /** Mirrors DOM isTrusted; only host-minted events are true. */
  readonly isTrusted: boolean;
}

export interface SelectionChangeEvent extends SessionEventBase {
  readonly type: 'selection';
  readonly selectionId: number;
  /** Fresh tickets replacing every previously issued ticket. */
  readonly tickets: readonly ReadonlyFileTicket[];
}

export interface SelectionCancelEvent extends SessionEventBase {
  readonly type: 'cancel';
  readonly selectionId: number;
}

export interface DocumentNavigateEvent extends SessionEventBase {
  readonly type: 'navigate';
  readonly fromDocumentId: string;
  readonly toDocumentId: string;
}

export interface SessionDisposeEvent extends SessionEventBase {
  readonly type: 'dispose';
  readonly documentId: string;
}

export type SessionEvent =
  | SelectionChangeEvent
  | SelectionCancelEvent
  | DocumentNavigateEvent
  | SessionDisposeEvent;

export type SessionListener<E extends SessionEvent = SessionEvent> = (event: E) => void;

/**
 * File object as known to the trusted host. Page scripts never receive a
 * reference to this object, only tickets derived from it.
 */
export interface StoredFileObject {
  readonly descriptor: FileObjectDescriptor;
  readonly bytes: Uint8Array;
}
