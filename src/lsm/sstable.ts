import { createHash } from 'node:crypto';

export interface Entry {
  key: string;
  // null value is a tombstone (deleted key)
  value: string | null;
}

export interface SSTableData {
  entries: Entry[];
  checksum: string;
}

export function sha256(buf: Buffer | string): string {
  return createHash('sha256').update(buf).digest('hex');
}

// SSTable wire format: newline-delimited JSON entries, terminated by a
// footer record carrying the entry count and a checksum of the payload.
// Once written, a table is NEVER modified in place (LSM immutability).
export function encodeSSTable(entries: Entry[]): Buffer {
  const sorted = [...entries].sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  const payload = sorted.map((e) => JSON.stringify([e.key, e.value])).join('\n');
  const footer = JSON.stringify({ __footer__: { count: sorted.length, sha256: sha256(payload) } });
  return Buffer.from(payload + (payload.length > 0 ? '\n' : '') + footer + '\n', 'utf8');
}

export function decodeSSTable(buf: Buffer): SSTableData {
  const text = buf.toString('utf8');
  const lines = text.split('\n').filter((l) => l.length > 0);
  if (lines.length === 0) throw new Error('empty sstable');
  const footerRaw = JSON.parse(lines[lines.length - 1]);
  if (!footerRaw.__footer__) throw new Error('missing sstable footer');
  const payloadLines = lines.slice(0, -1);
  const payload = payloadLines.join('\n');
  const checksum = sha256(payload);
  if (checksum !== footerRaw.__footer__.sha256) {
    throw new Error(`sstable payload checksum mismatch: expected ${footerRaw.__footer__.sha256}, got ${checksum}`);
  }
  if (payloadLines.length !== footerRaw.__footer__.count) {
    throw new Error(`sstable entry count mismatch: expected ${footerRaw.__footer__.count}, got ${payloadLines.length}`);
  }
  const entries: Entry[] = payloadLines.map((l) => {
    const [key, value] = JSON.parse(l) as [string, string | null];
    return { key, value };
  });
  return { entries, checksum };
}
