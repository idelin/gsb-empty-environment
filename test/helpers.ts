import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { buildSetFile, buildSetFileWithShards } from '../src/format';

let tmpCount = 0;

export function tmpDir(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ssidb-test-' + process.pid + '-' + tmpCount++ + '-'));
  return dir;
}

export function writeSet(dir: string, setId: string, version: string, ids: number[], shardSize: number): string {
  const file = path.join(dir, setId + '-' + version + '.ssidb');
  fs.writeFileSync(file, buildSetFile(setId, version, ids, shardSize));
  return file;
}

export function writeSetWithShards(
  dir: string,
  setId: string,
  version: string,
  shards: { rangeStart: number; rangeEnd: number; ids: number[] }[],
): string {
  const file = path.join(dir, setId + '-' + version + '.ssidb');
  fs.writeFileSync(file, buildSetFileWithShards(setId, version, shards));
  return file;
}

export function range(start: number, endExclusive: number, step = 1): number[] {
  const out: number[] = [];
  for (let i = start; i < endExclusive; i += step) out.push(i);
  return out;
}

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function randomUniqueIds(count: number, maxExclusive: number, seed: number): number[] {
  const rand = mulberry32(seed);
  const set = new Set<number>();
  while (set.size < count) set.add(Math.floor(rand() * maxExclusive));
  return [...set].sort((a, b) => a - b);
}
