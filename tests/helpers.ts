import { computeEventId } from '../src/hash.ts';
import type { Event, SchnorrVerifier } from '../src/types.ts';

export interface UnsignedEvent {
  pubkey: string;
  created_at: number;
  kind: number;
  tags: string[][];
  content: string;
}

export const PUBKEY_A = 'a1'.repeat(32);
export const PUBKEY_B = 'b2'.repeat(32);

/**
 * Builds an event carrying a *correct* NIP-01 id, so tests can focus on
 * individual checks by mutating one field afterwards.
 */
export async function makeEvent(
  partial: Partial<UnsignedEvent> & { sig?: string } = {},
): Promise<Event> {
  const unsigned: UnsignedEvent = {
    pubkey: partial.pubkey ?? PUBKEY_A,
    created_at: partial.created_at ?? 1_700_000_000,
    kind: partial.kind ?? 1,
    tags: partial.tags ?? [],
    content: partial.content ?? '',
  };
  const id = await computeEventId(unsigned);
  return { ...unsigned, id, sig: partial.sig ?? 'cc'.repeat(64) };
}

/**
 * Mock Schnorr verifier. Rejects ids/pubkeys placed on block lists; throws
 * when an id is placed on the throw list; accepts everything else. This
 * stands in for the caller-injected BIP-340 implementation.
 */
export function mockVerifier(options: {
  rejectIds?: string[];
  rejectPubkeys?: string[];
  throwForIds?: string[];
} = {}): SchnorrVerifier & { calls: Array<{ id: string; pubkey: string; sig: string }> } {
  const calls: Array<{ id: string; pubkey: string; sig: string }> = [];
  const rejectIds = new Set(options.rejectIds ?? []);
  const rejectPubkeys = new Set(options.rejectPubkeys ?? []);
  const throwForIds = new Set(options.throwForIds ?? []);
  const verifier = (id: string, pubkey: string, sig: string): boolean => {
    calls.push({ id, pubkey, sig });
    if (throwForIds.has(id)) {
      throw new Error('verifier boom');
    }
    return !rejectIds.has(id) && !rejectPubkeys.has(pubkey);
  };
  return Object.assign(verifier, { calls });
}
