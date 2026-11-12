import {
  BrowsingContext,
  SandboxDocument,
  type Clock,
  type InstanceGeneration,
  type SecurityOrigin,
  type StoragePartition,
} from "../index.ts";

export function origin(host = "app.example", scheme = "https", port: number | null = null): SecurityOrigin {
  return { scheme, host, port };
}

export function partition(id: string): StoragePartition {
  return { id };
}

export function generation(id: string): InstanceGeneration {
  return { id };
}

export function document(
  partitionId: string,
  generationId = "gen-1",
  host = "app.example",
): SandboxDocument {
  return new SandboxDocument(origin(host), partition(partitionId), generation(generationId));
}

export function context(
  partitionId: string,
  generationId = "gen-1",
  host = "app.example",
): BrowsingContext {
  return new BrowsingContext(document(partitionId, generationId, host));
}

export class FakeClock implements Clock {
  current: number;

  constructor(start = 1_000) {
    this.current = start;
  }

  now(): number {
    return this.current;
  }

 advance(ms: number): void {
    this.current += ms;
  }
}

export function bytes(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

export async function drain(stream: {
  read(): Promise<Uint8Array | null>;
}): Promise<Uint8Array> {
  const chunks: Uint8Array[] = [];
  for (;;) {
    const chunk = await stream.read();
    if (chunk === null) {
      break;
    }
    chunks.push(chunk);
  }
  const total = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return out;
}

export function decode(data: Uint8Array): string {
  return new TextDecoder().decode(data);
}

/** Token source that replays a scripted sequence, then UUIDs. */
export function scriptedTokens(tokens: string[]): () => string {
  const queue = [...tokens];
  return () => queue.shift() ?? crypto.randomUUID();
}
