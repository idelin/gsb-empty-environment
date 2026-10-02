import { Buffer } from 'node:buffer';
import type { Op, ReplicaId } from './document.ts';
import type { VersionVector } from './versionVector.ts';

/**
 * Wire protocol for incremental delta sync.
 *
 * - SYNC_REQUEST: "here is what I have (`have`); send me what I'm missing".
 * - DELTA: a contiguous delta slice `ops` on top of `base`; `version` is the
 *   sender's version vector after the slice. For a full-state transfer,
 *   `base` is simply the zero vector — full state and delta share one format.
 * - ACK: receiver confirms its version vector; the sender records it as the
 *   peer's acknowledgment watermark and will only send deltas above it.
 */
export type Message =
  | { type: 'sync-request'; from: ReplicaId; have: VersionVector }
  | {
      type: 'delta';
      from: ReplicaId;
      base: VersionVector;
      ops: Op[];
      version: VersionVector;
    }
  | { type: 'ack'; from: ReplicaId; version: VersionVector };

export function encodeMessage(msg: Message): string {
  return JSON.stringify(msg);
}

export function decodeMessage(json: string): Message {
  return JSON.parse(json) as Message;
}

/** Exact on-the-wire size in bytes (UTF-8). */
export function messageBytes(msg: Message): number {
  return Buffer.byteLength(encodeMessage(msg), 'utf8');
}
