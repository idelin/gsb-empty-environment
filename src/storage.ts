import { promises as fs } from 'node:fs';
import { createHash } from 'node:crypto';
import { dirname } from 'node:path';

/** fsync a directory so a rename/create inside it is durable. */
export async function fsyncDirectory(dir: string): Promise<void> {
  const handle = await fs.open(dir, 'r');
  try {
    await handle.sync();
  } finally {
    await handle.close();
  }
}

/**
 * Durable atomic file write: write tmp file, fsync it, rename over the target,
 * then fsync the containing directory. A crash can leave the old contents or
 * the new contents, never a torn file.
 */
export async function writeFileAtomic(path: string, data: string | Uint8Array): Promise<void> {
  const tmp = `${path}.tmp`;
  const handle = await fs.open(tmp, 'w');
  try {
    await handle.writeFile(data);
    await handle.sync();
  } finally {
    await handle.close();
  }
  await fs.rename(tmp, path);
  await fsyncDirectory(dirname(path));
}

/** Filesystem-safe, collision-resistant encoding of an external identifier. */
export function safeName(id: string): string {
  const encoded = encodeURIComponent(id);
  if (encoded.length > 0 && encoded.length <= 80) return encoded;
  return `h-${createHash('sha256').update(id).digest('hex').slice(0, 32)}`;
}

export async function pathExists(path: string): Promise<boolean> {
  try {
    await fs.access(path);
    return true;
  } catch {
    return false;
  }
}
