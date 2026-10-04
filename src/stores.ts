import { digestOf } from './digest.ts';

interface LocalEntry {
  version: number;
  bytes: string;
  handles: number;
}

/**
 * Hot tier. Tracks versions and open read handles.
 * Safety rule: an object with open handles cannot be deleted.
 *
 * Version numbers are monotonic per object and survive deletion of the
 * local copy (like a metadata catalog's generation number): after a
 * reclaim, a rewritten object must NOT restart at v1, or the cold
 * tier's last-writer-wins guard would reject its uploads forever.
 */
export class LocalStore {
  private entries = new Map<string, LocalEntry>();
  private versions = new Map<string, number>();

  /** Write a new version; returns the new version number. */
  write(id: string, bytes: string): number {
    const version = (this.versions.get(id) ?? 0) + 1;
    this.versions.set(id, version);
    const handles = this.entries.get(id)?.handles ?? 0;
    this.entries.set(id, { version, bytes, handles });
    return version;
  }

  has(id: string): boolean {
    return this.entries.has(id);
  }

  versionOf(id: string): number | null {
    return this.entries.get(id)?.version ?? null;
  }

  bytesOf(id: string): string | null {
    return this.entries.get(id)?.bytes ?? null;
  }

  digest(id: string): string | null {
    const e = this.entries.get(id);
    return e ? digestOf(e.bytes) : null;
  }

  handleCount(id: string): number {
    return this.entries.get(id)?.handles ?? 0;
  }

  /** Open a read handle. Fails if the object is gone locally. */
  acquire(id: string): boolean {
    const e = this.entries.get(id);
    if (!e) return false;
    e.handles += 1;
    return true;
  }

  release(id: string): void {
    const e = this.entries.get(id);
    if (!e || e.handles === 0) return;
    e.handles -= 1;
  }

  /**
   * Delete the local copy. Refuses while handles are open — callers
   * (the migrator) must defer deletion until handles drain.
   */
  delete(id: string): boolean {
    const e = this.entries.get(id);
    if (!e || e.handles > 0) return false;
    this.entries.delete(id);
    return true;
  }
}

interface RemoteEntry {
  version: number;
  bytes: string;
}

/** Cold tier. Last-writer-wins by version; supports corruption injection. */
export class RemoteStore {
  private objects = new Map<string, RemoteEntry>();

  /** Apply an uploaded version; stale uploads never overwrite newer data. */
  put(id: string, version: number, bytes: string): void {
    const existing = this.objects.get(id);
    if (existing && version < existing.version) return;
    this.objects.set(id, { version, bytes });
  }

  read(id: string): RemoteEntry | null {
    return this.objects.get(id) ?? null;
  }

  digest(id: string): string | null {
    const e = this.objects.get(id);
    return e ? digestOf(e.bytes) : null;
  }

  /** Fault injection: silently flip one byte, keeping the version tag. */
  corrupt(id: string): boolean {
    const e = this.objects.get(id);
    if (!e) return false;
    const i = e.bytes.length > 1 ? 1 : 0;
    const flipped = e.bytes.charCodeAt(i) ^ 0x01;
    e.bytes = e.bytes.slice(0, i) + String.fromCharCode(flipped) + e.bytes.slice(i + 1);
    return true;
  }
}
