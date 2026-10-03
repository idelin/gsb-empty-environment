// Key <-> nibble-path encoding. Keys are UTF-8 strings; each byte becomes
// two nibbles so the trie branches on 4-bit symbols (16-way branch nodes).

export type Nibbles = number[];

export function keyToNibbles(key: string): Nibbles {
  const bytes = Buffer.from(key, 'utf8');
  const nibbles: Nibbles = new Array(bytes.length * 2);
  for (let i = 0; i < bytes.length; i++) {
    nibbles[i * 2] = bytes[i] >> 4;
    nibbles[i * 2 + 1] = bytes[i] & 0x0f;
  }
  return nibbles;
}

export function nibblesToKey(nibbles: Nibbles): string {
  if (nibbles.length % 2 !== 0) {
    throw new Error('odd-length nibble path cannot be decoded to a key');
  }
  const bytes = Buffer.alloc(nibbles.length / 2);
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = (nibbles[i * 2] << 4) | nibbles[i * 2 + 1];
  }
  return bytes.toString('utf8');
}

export function nibblesToHex(nibbles: Nibbles): string {
  return nibbles.map((n) => n.toString(16)).join('');
}

export function hexToNibbles(hex: string): Nibbles {
  const out: Nibbles = new Array(hex.length);
  for (let i = 0; i < hex.length; i++) out[i] = parseInt(hex[i], 16);
  return out;
}

export function commonPrefixLength(a: Nibbles, b: Nibbles): number {
  const n = Math.min(a.length, b.length);
  let i = 0;
  while (i < n && a[i] === b[i]) i++;
  return i;
}

export function nibblesEqual(a: Nibbles, b: Nibbles): boolean {
  return a.length === b.length && commonPrefixLength(a, b) === a.length;
}
