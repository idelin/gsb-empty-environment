// FNV-1a 32-bit digest over UTF-16 code units. Deterministic, dependency-free.
export function digestOf(bytes: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < bytes.length; i++) {
    const code = bytes.charCodeAt(i);
    hash ^= code & 0xff;
    hash = Math.imul(hash, 0x01000193);
    hash ^= (code >>> 8) & 0xff;
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, '0');
}
