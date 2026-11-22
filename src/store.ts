export interface FileObjectMeta {
  readonly objectId: string;
  readonly name: string;
  readonly size: number;
}

interface StoredObject {
  readonly name: string;
  readonly bytes: Uint8Array;
}

/**
 * Backing store for file objects, standing in for the browser/OS side.
 * Object ids are plain strings: they name objects but are NOT capabilities.
 * Only a SelectionSession can turn an id (via a trusted event) into a ticket.
 */
export class FileObjectStore {
  #objects = new Map<string, StoredObject>();
  #nextId = 1;

  register(name: string, bytes: Uint8Array): string {
    const objectId = `obj-${this.#nextId++}`;
    this.#objects.set(objectId, { name, bytes: new Uint8Array(bytes) });
    return objectId;
  }

  describe(objectId: string): FileObjectMeta | undefined {
    const entry = this.#objects.get(objectId);
    if (!entry) return undefined;
    return { objectId, name: entry.name, size: entry.bytes.length };
  }

  snapshot(objectId: string): Uint8Array | undefined {
    const entry = this.#objects.get(objectId);
    if (!entry) return undefined;
    return new Uint8Array(entry.bytes);
  }
}
