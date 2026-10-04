import { afterAll, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ChunkSource, FileChunkSource, MemoryChunkSource } from "../src/source";
import { commitSource, proveRange } from "../src/prover";
import { verifyRange } from "../src/verifier";
import { CHUNK_SIZE, makeBytes } from "./helpers";

/** Records every index the verifier asks for and every byte it consumes. */
class TrackingSource implements ChunkSource {
  requested: number[] = [];
  bytesServed = 0;
  constructor(private readonly inner: ChunkSource) {}

  async getChunk(index: number): Promise<Uint8Array | undefined> {
    this.requested.push(index);
    const data = await this.inner.getChunk(index);
    if (data !== undefined) this.bytesServed += data.length;
    return data;
  }
}

const dirs: string[] = [];
afterAll(async () => {
  for (const d of dirs) await rm(d, { recursive: true, force: true });
});

describe("the verifier never reads the whole file", () => {
  test("only the requested range is pulled from the chunk source", async () => {
    const length = 100 * CHUNK_SIZE + 13; // 101 chunks
    const data = makeBytes(length, 71);
    const chunks = new Map<number, Uint8Array>();
    for (let i = 0; i < Math.ceil(length / CHUNK_SIZE); i++) {
      chunks.set(i, data.slice(i * CHUNK_SIZE, Math.min(length, (i + 1) * CHUNK_SIZE)));
    }
    const proven = await commitSource(new MemoryChunkSource(chunks), length, CHUNK_SIZE);
    const tracking = new TrackingSource(new MemoryChunkSource(chunks));
    const proofs = new Map(proveRange(proven, 40, 45).map((p) => [p.index, p]));

    const report = await verifyRange({
      commitment: proven.commitment,
      header: proven.header,
      start: 40,
      end: 45,
      chunks: tracking,
      proofs,
    });

    expect(report.ok).toEqual([40, 41, 42, 43, 44]);
    // The verifier touched exactly five chunks — not the other 96.
    expect([...tracking.requested].sort((a, b) => a - b)).toEqual([40, 41, 42, 43, 44]);
    expect(tracking.bytesServed).toBe(5 * CHUNK_SIZE);
    expect(tracking.bytesServed).toBeLessThan(length / 20);
  });

  test("FileChunkSource reads only the requested byte ranges from disk", async () => {
    const dir = await mkdtemp(join(tmpdir(), "cfv-"));
    dirs.push(dir);
    const length = 256 * CHUNK_SIZE + 31; // ~16 MiB worth of chunks at 64 KiB
    const data = makeBytes(length, 72);
    const file = join(dir, "big.bin");
    await writeFile(file, data);

    const source = await FileChunkSource.open(file, CHUNK_SIZE);
    const tracking = new TrackingSource(source);
    const proven = await commitSource(
      new MemoryChunkSource(new Map(
        Array.from({ length: source.count }, (_, i) => [
          i,
          data.slice(i * CHUNK_SIZE, Math.min(length, (i + 1) * CHUNK_SIZE)),
        ]),
      )),
      length,
      CHUNK_SIZE,
    );
    const proofs = new Map(proveRange(proven, 100, 103).map((p) => [p.index, p]));

    const report = await verifyRange({
      commitment: proven.commitment,
      header: proven.header,
      start: 100,
      end: 103,
      chunks: tracking,
      proofs,
    });

    expect(report.ok).toEqual([100, 101, 102]);
    expect(tracking.requested).toEqual([100, 101, 102]);
    // 3 chunks read from a 257-chunk file.
    expect(tracking.bytesServed).toBe(3 * CHUNK_SIZE);
    expect(tracking.bytesServed).toBeLessThan(length / 80);
  });

  test("FileChunkSource returns exactly the on-disk chunk bytes", async () => {
    const dir = await mkdtemp(join(tmpdir(), "cfv-"));
    dirs.push(dir);
    const length = 3 * CHUNK_SIZE + 9;
    const data = makeBytes(length, 73);
    const file = join(dir, "small.bin");
    await writeFile(file, data);

    const source = await FileChunkSource.open(file, CHUNK_SIZE);
    expect(source.count).toBe(4);
    for (let i = 0; i < 4; i++) {
      const chunk = await source.getChunk(i);
      expect(chunk).toEqual(data.slice(i * CHUNK_SIZE, Math.min(length, (i + 1) * CHUNK_SIZE)));
    }
    expect(await source.getChunk(4)).toBeUndefined();
    expect(await source.getChunk(-1)).toBeUndefined();
  });
});
