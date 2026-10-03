/**
 * In-memory write buffer. Keys are stored by their latin1 byte-string
 * encoding so arbitrary byte sequences compare and dedupe correctly.
 */
export class MemTable {
  private map = new Map<string, Buffer>();
  private bytes = 0;

  put(key: Buffer, value: Buffer): void {
    const id = key.toString('latin1');
    this.bytes += key.length + value.length; // approximate; duplicates may double-count
    this.map.set(id, Buffer.from(value));
  }

  get(key: Buffer): Buffer | null {
    return this.map.get(key.toString('latin1')) ?? null;
  }

  get size(): number {
    return this.map.size;
  }

  get approximateBytes(): number {
    return this.bytes;
  }

  clear(): void {
    this.map.clear();
    this.bytes = 0;
  }

  /** Sorted entries, optionally filtered to a prefix. */
  *entries(prefix?: Buffer): Generator<{ key: Buffer; value: Buffer }> {
    const ids = [...this.map.keys()].sort();
    for (const id of ids) {
      const key = Buffer.from(id, 'latin1');
      if (prefix && !key.subarray(0, prefix.length).equals(prefix)) {
        if (Buffer.compare(key, prefix) > 0) break; // sorted: no more matches
        continue;
      }
      yield { key, value: this.map.get(id)! };
    }
  }
}
