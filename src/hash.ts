import { createHash } from 'node:crypto';

export function hash32(input: string): number {
  const digest = createHash('sha1').update(input, 'utf8').digest();
  return digest.readUInt32BE(0);
}
