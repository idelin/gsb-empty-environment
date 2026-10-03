import { parentPort } from "worker_threads";
import { PageInput, serializeChunk } from "./format";

export interface ChunkTask {
  chunkIndex: number;
  pages: PageInput[];
}

export interface ChunkResult {
  chunkIndex: number;
  chunkHash: string;
  pageHashes: string[];
  buffer: Buffer;
}

if (parentPort) {
  const port = parentPort;
  port.on("message", (task: ChunkTask) => {
    const result = serializeChunk(task.chunkIndex, task.pages);
    const message: ChunkResult = {
      chunkIndex: task.chunkIndex,
      chunkHash: result.chunkHash,
      pageHashes: result.pageHashes,
      buffer: result.buffer,
    };
    port.postMessage(message);
  });
}
