import { digestOf } from './digest';
import type { Prng } from './prng';

export interface Copy {
  version: number;
  bytes: string;
  digest: string;
}

export function makeCopy(version: number, bytes: string): Copy {
  return { version, bytes, digest: digestOf(bytes) };
}

// A copy is valid only if its recorded digest matches its actual bytes.
export function copyValid(copy: Copy): boolean {
  return digestOf(copy.bytes) === copy.digest;
}

export interface Receipt {
  objectId: string;
  version: number;
  digest: string;
  issuedAt: number;
}

// Hot tier. Also tracks open read handles: a local copy must not be
// reclaimed while any reader holds a handle to it.
export class LocalStore {
  private copies = new Map<string, Copy>();
  private versions = new Map<string, number>();
  private handles = new Map<string, number>();

  write(objectId: string, bytes: string): Copy {
    const version = (this.versions.get(objectId) ?? 0) + 1;
    this.versions.set(objectId, version);
    const copy = makeCopy(version, bytes);
    this.copies.set(objectId, copy);
    return copy;
  }

  read(objectId: string): Copy | undefined {
    return this.copies.get(objectId);
  }

  remove(objectId: string): Copy | undefined {
    if (this.openHandles(objectId) > 0) {
      throw new Error(`cannot remove ${objectId}: ${this.openHandles(objectId)} open handle(s)`);
    }
    const copy = this.copies.get(objectId);
    this.copies.delete(objectId);
    return copy;
  }

  openHandle(objectId: string): void {
    if (!this.copies.has(objectId)) {
      throw new Error(`cannot open handle on ${objectId}: no local copy`);
    }
    this.handles.set(objectId, this.openHandles(objectId) + 1);
  }

  closeHandle(objectId: string): void {
    const open = this.openHandles(objectId);
    if (open <= 0) throw new Error(`no open handle on ${objectId}`);
    this.handles.set(objectId, open - 1);
  }

  openHandles(objectId: string): number {
    return this.handles.get(objectId) ?? 0;
  }
}

// Cold tier (migration target). Stores copies and issues receipts.
export class ColdStore {
  private copies = new Map<string, Copy>();

  put(objectId: string, copy: Copy, now: number): Receipt {
    this.copies.set(objectId, { ...copy });
    return { objectId, version: copy.version, digest: copy.digest, issuedAt: now };
  }

  read(objectId: string): Copy | undefined {
    return this.copies.get(objectId);
  }

  // Fault injection: bit-rot. Mutates bytes without touching the recorded
  // digest, so copyValid() starts failing on read-back.
  corrupt(objectId: string, rng: Prng): boolean {
    const copy = this.copies.get(objectId);
    if (!copy || copy.bytes.length === 0) return false;
    const at = Math.floor(rng() * copy.bytes.length);
    const flipped = String.fromCharCode(copy.bytes.charCodeAt(at) ^ 0x5a);
    copy.bytes = copy.bytes.slice(0, at) + flipped + copy.bytes.slice(at + 1);
    return true;
  }
}
