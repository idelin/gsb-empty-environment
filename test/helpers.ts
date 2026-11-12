import {
  SIGNATURE_HEADER,
  type CacheBackend,
  type CacheRequestLike,
} from "../src/cache-signature.ts";

export const encoder = new TextEncoder();
export const encode = (value: string): Uint8Array => encoder.encode(value);

export const TEAM_A = "team-alpha";
export const TEAM_B = "team-beta";
export const KEY_A = encode("secret-key-team-alpha");
export const KEY_B = encode("secret-key-team-beta");
export const HASH_A = "0123456789abcdef";
export const HASH_B = "fedcba9876543210";

export class MemoryBackend implements CacheBackend {
  readonly stored = new Map<string, Uint8Array>();
  readonly storeCalls: Array<{ teamId: string; taskHash: string; length: number }> = [];

  storeArtifact(teamId: string, taskHash: string, bytes: Uint8Array): void {
    this.storeCalls.push({ teamId, taskHash, length: bytes.length });
    this.stored.set(`${teamId}/${taskHash}`, bytes);
  }

  loadArtifact(teamId: string, taskHash: string): Uint8Array | null {
    return this.stored.get(`${teamId}/${taskHash}`) ?? null;
  }

  tamper(teamId: string, taskHash: string): void {
    const key = `${teamId}/${taskHash}`;
    const current = this.stored.get(key);
    if (!current) {
      throw new Error("cannot tamper: artifact missing");
    }
    const modified = new Uint8Array(current);
    modified[0] ^= 0x01;
    this.stored.set(key, modified);
  }
}

export const cacheUrl = (teamId: string, taskHash: string): string =>
  `https://cache.example.com/cache/artifacts/${teamId}/${taskHash}`;

export function makeRequest(
  method: "GET" | "PUT",
  teamId: string,
  taskHash: string,
  signature: string | null,
  body?: ReadableStream<Uint8Array> | Uint8Array,
): Request {
  const headers = new Headers();
  if (signature !== null) {
    headers.set(SIGNATURE_HEADER, signature);
  }
  if (method === "PUT") {
    return new Request(cacheUrl(teamId, taskHash), {
      method,
      headers,
      body: body as BodyInit,
      duplex: "half",
    });
  }
  return new Request(cacheUrl(teamId, taskHash), { method, headers });
}

export function chunked(data: Uint8Array, chunkSize: number): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(controller) {
      for (let offset = 0; offset < data.length; offset += chunkSize) {
        controller.enqueue(data.subarray(offset, Math.min(offset + chunkSize, data.length)));
      }
      controller.close();
    },
  });
}

export class HangingRequest implements CacheRequestLike {
  readonly method = "PUT";
  readonly url: string;
  readonly headers: Headers;
  readonly body: ReadableStream<Uint8Array>;

  constructor(teamId: string, taskHash: string, signature: string) {
    this.url = cacheUrl(teamId, taskHash);
    this.headers = new Headers({ [SIGNATURE_HEADER]: signature });
    this.body = new ReadableStream({
      pull() {
        return new Promise<void>(() => {});
      },
    });
  }
}
