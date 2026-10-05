export interface BufferIdentity {
  readonly bufferId: number;
  readonly version: number;
  readonly byteOffset: number;
  readonly byteLength: number;
}

interface BufferRecord {
  id: number;
  version: number;
}

/**
 * Tracks the real version of the *underlying ArrayBuffer*, so two views
 * (e.g. subarrays) over the same buffer always observe the same version.
 * Version records live in a WeakMap keyed by the buffer itself: a view can
 * never carry a stale private version that disagrees with its siblings.
 */
export class VersionRegistry {
  private readonly records = new WeakMap<ArrayBufferLike, BufferRecord>();
  private nextId = 1;

  private recordFor(buffer: ArrayBufferLike): BufferRecord {
    let record = this.records.get(buffer);
    if (record === undefined) {
      record = { id: this.nextId++, version: 0 };
      this.records.set(buffer, record);
    }
    return record;
  }

  /** Identity of a view: buffer id + buffer version + the view's window. */
  identityOf(view: ArrayBufferView): BufferIdentity {
    const record = this.recordFor(view.buffer);
    return {
      bufferId: record.id,
      version: record.version,
      byteOffset: view.byteOffset,
      byteLength: view.byteLength,
    };
  }

  versionOf(view: ArrayBufferView): number {
    return this.recordFor(view.buffer).version;
  }

  /**
   * Declare that the contents of the buffer backing `view` changed.
   * Every view over the same buffer immediately sees the new version.
   */
  bump(view: ArrayBufferView): number {
    const record = this.recordFor(view.buffer);
    record.version += 1;
    return record.version;
  }
}
