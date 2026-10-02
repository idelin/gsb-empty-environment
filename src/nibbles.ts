export type Nibbles = number[];

export function keyToNibbles(key: string): Nibbles {
  const bytes = Buffer.from(key, 'utf8');
  const nibbles: Nibbles = [];
  for (const byte of bytes) {
    nibbles.push(byte >> 4, byte & 0x0f);
  }
  return nibbles;
}

export function nibblesToHex(nibbles: Nibbles): string {
  return nibbles.map((n) => n.toString(16)).join('');
}

export function hexToNibbles(hex: string): Nibbles {
  const nibbles: Nibbles = [];
  for (const ch of hex) {
    nibbles.push(parseInt(ch, 16));
  }
  return nibbles;
}

export function commonPrefixLength(a: Nibbles, b: Nibbles): number {
  let i = 0;
  while (i < a.length && i < b.length && a[i] === b[i]) i++;
  return i;
}
