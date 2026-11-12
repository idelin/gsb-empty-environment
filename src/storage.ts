import type { StoredFileObject } from './types.ts';

/**
 * Trusted-side file storage. Implementations are part of the picker host;
 * page code must never be able to call it with a raw path or object id.
 */
export interface FileStorage {
  get(objectId: string): StoredFileObject | undefined;
}

export class InMemoryFileStorage implements FileStorage {
  readonly #files = new Map<string, StoredFileObject>();

  add(file: StoredFileObject): void {
    this.#files.set(file.descriptor.objectId, file);
  }

  get(objectId: string): StoredFileObject | undefined {
    return this.#files.get(objectId);
  }
}
