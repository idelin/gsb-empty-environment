import { createHash } from 'node:crypto';

/** Content digest used to verify copies (truncated sha256 for readable traces). */
export function digestOf(bytes: string): string {
  return createHash('sha256').update(bytes).digest('hex').slice(0, 16);
}
