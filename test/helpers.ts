import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { TileStore, sha256Hash, type HashFn } from '../src/store.ts';
import { Document } from '../src/document.ts';
import type { Tile } from '../src/image.ts';

export function tempDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'tilestore-'));
}

export function openStore(dir: string, hashFn: HashFn = sha256Hash): TileStore {
  return TileStore.open(dir, hashFn);
}

export function solidTile(size: number, r: number, g: number, b: number, a = 255): Tile {
  const pixels = new Uint8Array(size * size * 4);
  for (let i = 0; i < size * size; i++) {
    pixels[i * 4] = r;
    pixels[i * 4 + 1] = g;
    pixels[i * 4 + 2] = b;
    pixels[i * 4 + 3] = a;
  }
  return { width: size, height: size, pixels };
}

export function makeDoc(dir: string, tileSize = 8, width = 16, height = 16): { store: TileStore; doc: Document } {
  const store = openStore(dir);
  const doc = Document.create(store, width, height, tileSize);
  return { store, doc };
}

export const weakHash: HashFn = (data) => {
  let sum = 0;
  for (const byte of data) sum = (sum + byte) % 4;
  return `weak-${sum}`;
};
