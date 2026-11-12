import { HmacSha256, constantTimeEqual, parseLowerHex32, toLowerHex } from "./hmac.ts";

export const SIGNATURE_HEADER = "x-cache-signature";

const TASK_HASH_PATTERN = /^[0-9a-f]{16}$/;
const SEGMENT_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const NULL = 0x00;

export type VerifyFailureReason =
  | "missing-signature"
  | "malformed-signature"
  | "unknown-team"
  | "signature-mismatch";

export interface VerifySuccess {
  readonly ok: true;
  readonly teamId: string;
  readonly taskHash: string;
  readonly bytes: Uint8Array;
}

export interface VerifyFailure {
  readonly ok: false;
  readonly reason: VerifyFailureReason;
}

export type VerifyResult = VerifySuccess | VerifyFailure;

/**
 * Minimal structural shape of a fetch Request used by the handler. Real
 * Request objects satisfy it, and lightweight test doubles can implement it.
 */
export interface CacheRequestLike {
  readonly method: string;
  readonly url: string;
  readonly headers: {
    get(name: string): string | null;
  };
  readonly body: ReadableStream<Uint8Array> | null;
}

export type SecretResolver =
  | Uint8Array
  | Map<string, Uint8Array>
  | Record<string, Uint8Array>
  | ((teamId: string) => Uint8Array | null | undefined);

export interface CacheBackend {
  /**
   * Persist an already-verified artifact. This is the unpack/store callback
   * and only receives bytes after signature verification succeeded.
   */
  storeArtifact(
    teamId: string,
    taskHash: string,
    bytes: Uint8Array,
  ): Promise<void> | void;

  loadArtifact(
    teamId: string,
    taskHash: string,
  ): Promise<Uint8Array | null> | Uint8Array | null;
}

export interface CacheSignatureOptions {
  readonly backend: CacheBackend;
  readonly secrets: SecretResolver;
  /** Optional header name override; defaults to x-cache-signature. */
  readonly signatureHeader?: string;
}

export function isValidTaskHash(taskHash: string): boolean {
  return TASK_HASH_PATTERN.test(taskHash);
}

function isValidTeamId(teamId: string): boolean {
  return SEGMENT_PATTERN.test(teamId);
}

function resolveSecret(
  resolver: SecretResolver,
  teamId: string,
): Uint8Array | null {
  if (resolver instanceof Uint8Array) {
    return resolver;
  }
  if (resolver instanceof Map) {
    return resolver.get(teamId) ?? null;
  }
  if (typeof resolver === "function") {
    return resolver(teamId) ?? null;
  }
  return resolver[teamId] ?? null;
}

/**
 * Canonical signed message: teamId || 0x00 || taskHash || 0x00 || artifact.
 * The 0x00 delimiters are unambiguous because teamId and taskHash are
 * restricted to non-NUL ASCII characters.
 */
function createSigner(key: Uint8Array): HmacSha256 {
  return new HmacSha256(key);
}

function writePrefix(
  signer: HmacSha256,
  teamId: string,
  taskHash: string,
): void {
  const encoder = new TextEncoder();
  signer.update(encoder.encode(teamId));
  signer.update(new Uint8Array([NULL]));
  signer.update(encoder.encode(taskHash));
  signer.update(new Uint8Array([NULL]));
}

/** Client/test helper: produce the lowercase hex signature label. */
export function signArtifact(
  key: Uint8Array,
  teamId: string,
  taskHash: string,
  artifact: Uint8Array,
): string {
  const signer = createSigner(key);
  writePrefix(signer, teamId, taskHash);
  signer.update(artifact);
  return toLowerHex(signer.digest());
}

function verifyMac(
  key: Uint8Array,
  teamId: string,
  taskHash: string,
  artifact: Uint8Array,
  signature: Uint8Array,
): boolean {
  const signer = createSigner(key);
  writePrefix(signer, teamId, taskHash);
  signer.update(artifact);
  const expected = signer.digest();
  return constantTimeEqual(expected, signature);
}

function failure(reason: VerifyFailureReason): VerifyFailure {
  return { ok: false, reason };
}

/**
 * Incrementally verify a streamed artifact. The unpack/store callback never
 * sees bytes because chunks are only fed into the HMAC and buffered until the
 * full body has been authenticated.
 */
export class ArtifactVerifier {
  readonly #hmac: HmacSha256;
  readonly #chunks: Uint8Array[] = [];
  #length = 0;

  constructor(key: Uint8Array, teamId: string, taskHash: string) {
    this.#hmac = createSigner(key);
    writePrefix(this.#hmac, teamId, taskHash);
  }

  update(chunk: Uint8Array): void {
    this.#hmac.update(chunk);
    this.#chunks.push(chunk);
    this.#length += chunk.length;
  }

  finish(signatureLabel: string): { ok: boolean; bytes: Uint8Array } {
    const supplied = parseLowerHex32(signatureLabel);
    if (supplied === null) {
      return { ok: false, bytes: new Uint8Array(0) };
    }
    const expected = this.#hmac.digest();
    if (!constantTimeEqual(expected, supplied)) {
      return { ok: false, bytes: new Uint8Array(0) };
    }
    return { ok: true, bytes: this.#concat() };
  }

  #concat(): Uint8Array {
    const output = new Uint8Array(this.#length);
    let offset = 0;
    for (const chunk of this.#chunks) {
      output.set(chunk, offset);
      offset += chunk.length;
    }
    return output;
  }
}

/**
 * Drain a request body while authenticating it. No chunk is forwarded to any
 * downstream callback before finish() reports success.
 */
export async function readAndVerifyArtifact(
  body: ReadableStream<Uint8Array> | null,
  key: Uint8Array,
  teamId: string,
  taskHash: string,
  signatureLabel: string | null,
): Promise<VerifyResult> {
  if (signatureLabel === null || signatureLabel === "") {
    return failure("missing-signature");
  }
  const supplied = parseLowerHex32(signatureLabel);
  if (supplied === null) {
    return failure("malformed-signature");
  }

  const verifier = new ArtifactVerifier(key, teamId, taskHash);
  if (body !== null) {
    const reader = body.getReader();
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) {
          break;
        }
        if (value !== undefined && value.length > 0) {
          verifier.update(value);
        }
      }
    } finally {
      reader.releaseLock();
    }
  }

  const result = verifier.finish(signatureLabel);
  if (!result.ok) {
    return failure("signature-mismatch");
  }
  return { ok: true, teamId, taskHash, bytes: result.bytes };
}

function verifyBufferedArtifact(
  key: Uint8Array,
  teamId: string,
  taskHash: string,
  artifact: Uint8Array,
  signatureLabel: string | null,
): VerifyResult {
  if (signatureLabel === null || signatureLabel === "") {
    return failure("missing-signature");
  }
  const supplied = parseLowerHex32(signatureLabel);
  if (supplied === null) {
    return failure("malformed-signature");
  }
  if (!verifyMac(key, teamId, taskHash, artifact, supplied)) {
    return failure("signature-mismatch");
  }
  return { ok: true, teamId, taskHash, bytes: artifact };
}

interface Route {
  readonly teamId: string;
  readonly taskHash: string;
}

function parseRoute(urlValue: string): Route | null {
  let pathname: string;
  try {
    pathname = new URL(urlValue, "http://cache.local").pathname;
  } catch {
    return null;
  }
  const parts = pathname.split("/").filter((segment) => segment.length > 0);
  if (parts.length !== 4 || parts[0] !== "cache" || parts[1] !== "artifacts") {
    return null;
  }
  const [, , teamId, taskHash] = parts;
  if (!isValidTeamId(teamId) || !isValidTaskHash(taskHash)) {
    return null;
  }
  return { teamId, taskHash };
}

function errorResponse(status: number, code: string): Response {
  return new Response(JSON.stringify({ error: code }), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function failureStatus(reason: VerifyFailureReason): number {
  switch (reason) {
    case "missing-signature":
    case "malformed-signature":
      return 400;
    case "unknown-team":
    case "signature-mismatch":
      return 401;
  }
}

/**
 * Build a fetch handler implementing authenticated remote-cache access:
 *
 *   PUT  /cache/artifacts/:teamId/:taskHash  (upload, body = artifact)
 *   GET  /cache/artifacts/:teamId/:taskHash  (download)
 *
 * The signature travels in the x-cache-signature header on both methods.
 * On upload the body is authenticated before backend.storeArtifact runs;
 * on download the stored bytes are re-authenticated before they are returned,
 * which also catches post-upload tampering in the storage layer.
 */
export function createCacheSignatureHandler(
  options: CacheSignatureOptions,
): (request: CacheRequestLike) => Promise<Response> {
  const headerName = (options.signatureHeader ?? SIGNATURE_HEADER).toLowerCase();

  return async function handle(request: CacheRequestLike): Promise<Response> {
    if (request.method !== "PUT" && request.method !== "GET") {
      return errorResponse(405, "method-not-allowed");
    }

    const route = parseRoute(request.url);
    if (route === null) {
      return errorResponse(400, "bad-request");
    }
    const { teamId, taskHash } = route;

    const signatureLabel = request.headers.get(headerName);
    if (signatureLabel === null || signatureLabel === "") {
      return errorResponse(400, "missing-signature");
    }
    if (parseLowerHex32(signatureLabel) === null) {
      return errorResponse(400, "malformed-signature");
    }

    const key = resolveSecret(options.secrets, teamId);
    if (key === null) {
      return errorResponse(401, "unknown-team");
    }

    if (request.method === "PUT") {
      const result = await readAndVerifyArtifact(
        request.body,
        key,
        teamId,
        taskHash,
        signatureLabel,
      );
      if (!result.ok) {
        return errorResponse(failureStatus(result.reason), result.reason);
      }
      await options.backend.storeArtifact(teamId, taskHash, result.bytes);
      return new Response(null, { status: 204 });
    }

    const artifact = await options.backend.loadArtifact(teamId, taskHash);
    if (artifact === null) {
      return errorResponse(404, "artifact-not-found");
    }
    const result = verifyBufferedArtifact(
      key,
      teamId,
      taskHash,
      artifact,
      signatureLabel,
    );
    if (!result.ok) {
      return errorResponse(failureStatus(result.reason), result.reason);
    }
    return new Response(result.bytes, {
      status: 200,
      headers: { "content-type": "application/octet-stream" },
    });
  };
}
