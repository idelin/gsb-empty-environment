export { SelectionSession } from './session.ts';
export { TrustedFilePicker } from './picker.ts';
export { TicketRegistry } from './registry.ts';
export { InMemoryFileStorage, type FileStorage } from './storage.ts';
export type {
  FileObjectDescriptor,
  ReadonlyFileTicket,
  StoredFileObject,
  SessionEvent,
  SessionEventType,
  SessionEventMap,
  SessionListener,
  SelectionChangeEvent,
  SelectionCancelEvent,
  DocumentNavigateEvent,
  SessionDisposeEvent,
} from './types.ts';
export {
  TicketError,
  TicketRevokedError,
  TicketConsumedError,
  UntrustedEventError,
  SessionDisposedError,
  PathAccessError,
} from './errors.ts';

import type { FileObjectDescriptor, StoredFileObject } from './types.ts';

/** Host-side helper: register bytes under a stable object id. */
export function createFileObject(
  descriptor: FileObjectDescriptor,
  bytes: Uint8Array,
): StoredFileObject {
  return { descriptor: Object.freeze({ ...descriptor }), bytes };
}
