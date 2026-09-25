import { createHmac, randomBytes, timingSafeEqual } from 'node:crypto';

/**
 * In-memory key registry. Keys are generated/registered locally and never
 * leave the process: there is deliberately no hook for an external identity
 * or key service.
 */
export class KeyRegistry {
  private readonly keys = new Map<string, Buffer>();

  generate(principal: string): void {
    if (this.keys.has(principal)) {
      throw new Error(`key already registered for principal "${principal}"`);
    }
    this.keys.set(principal, randomBytes(32));
  }

  register(principal: string, secret: Buffer): void {
    if (this.keys.has(principal)) {
      throw new Error(`key already registered for principal "${principal}"`);
    }
    this.keys.set(principal, Buffer.from(secret));
  }

  has(principal: string): boolean {
    return this.keys.has(principal);
  }

  sign(principal: string, payload: string): string {
    const key = this.keys.get(principal);
    if (!key) {
      throw new Error(`no key registered for principal "${principal}"`);
    }
    return createHmac('sha256', key).update(payload, 'utf8').digest('hex');
  }

  verify(principal: string, payload: string, signature: string): boolean {
    const key = this.keys.get(principal);
    if (!key) {
      return false;
    }
    const expected = createHmac('sha256', key).update(payload, 'utf8').digest();
    const actual = Buffer.from(signature, 'hex');
    return expected.length === actual.length && timingSafeEqual(expected, actual);
  }
}

/** Deterministic JSON: object keys sorted recursively so signatures are stable. */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalize(item)).join(',')}]`;
  }
  const record = value as Record<string, unknown>;
  const parts = Object.keys(record)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${canonicalize(record[key])}`);
  return `{${parts.join(',')}}`;
}
