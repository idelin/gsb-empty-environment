import { open, stat } from "node:fs/promises";
import { chunkCount, chunkDataLength } from "./tree";

/**
 * Supplies chunk data on demand. The verifier only ever asks for the chunk
 * indices it is checking — it never requires the whole file.
 */
export interface ChunkSource {
  getChunk(index: number): Promise<Uint8Array | undefined>;
}

/** In-memory source, mainly for tests and bundling already-received chunks. */
export class MemoryChunkSource implements ChunkSource {
  constructor(private readonly chunks: Map<number, Uint8Array>) {}

  async getChunk(index: number): Promise<Uint8Array | undefined> {
    return this.chunks.get(index);
  }
}

/**
 * Reads individual chunks from a file with positioned reads. Only the
 * requested byte ranges are ever read; the file is never loaded in full.
 */
export class FileChunkSource implements ChunkSource {
  private constructor(
    private readonly filePath: string,
    private readonly fileLength: number,
    private readonly chunkSize: number,
  ) {}

  static async open(filePath: string, chunkSize: number): Promise<FileChunkSource> {
    const info = await stat(filePath);
    return new FileChunkSource(filePath, info.size, chunkSize);
  }

  get length(): number {
    return this.fileLength;
  }

  get count(): number {
    return chunkCount(this.fileLength, this.chunkSize);
  }

  async getChunk(index: number): Promise<Uint8Array | undefined> {
    if (index < 0 || index >= this.count) return undefined;
    const length = chunkDataLength(this.fileLength, this.chunkSize, index);
    const handle = await open(this.filePath, "r");
    try {
      const buffer = Buffer.alloc(length);
      await handle.read(buffer, 0, length, index * this.chunkSize);
      return new Uint8Array(buffer);
    } finally {
      await handle.close();
    }
  }
}
