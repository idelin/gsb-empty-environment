import { createHash } from 'node:crypto';

/**
 * A sealed placeholder hides a sensitive attribute value behind a commitment.
 * The plaintext never enters the evidence record; the field is independently
 * sealable per attribute.
 */
export interface SealedPlaceholder {
  sealed: true;
  commitment: string;
}

export type AttributeValue = number | SealedPlaceholder;
export type AttributeSnapshot = Record<string, AttributeValue>;

export function sealAttribute(attr: string, value: number, salt: string): SealedPlaceholder {
  const commitment = createHash('sha256').update(`${attr}:${value}:${salt}`).digest('hex');
  return { sealed: true, commitment };
}

export function isSealed(value: AttributeValue): value is SealedPlaceholder {
  return typeof value === 'object' && value !== null && (value as SealedPlaceholder).sealed === true;
}

function canonicalSnapshot(snapshot: AttributeSnapshot): unknown {
  const out: Record<string, unknown> = {};
  for (const key of Object.keys(snapshot).sort()) {
    const value = snapshot[key];
    out[key] = isSealed(value) ? { sealed: true, commitment: value.commitment } : value;
  }
  return out;
}

export function snapshotHash(snapshot: AttributeSnapshot): string {
  return createHash('sha256').update(JSON.stringify(canonicalSnapshot(snapshot))).digest('hex');
}
