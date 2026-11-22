import { cloneBytes } from "./canonical.ts";
import { ErrorCode, PipelineError, asPipelineError } from "./errors.ts";
import { digestHex } from "./hash.ts";
import type { AssetManifest } from "./manifest.ts";
import { manifestFingerprint } from "./manifest.ts";
import type { RepresentationVerification } from "./verify.ts";
import type { RevocationSet } from "./cache.ts";

const PROOF_BRAND = Symbol("gsb.verification-proof");
const CREDENTIAL_BRAND = Symbol("gsb.publication-credential");
const CREDENTIAL_ISSUE = Symbol("gsb.publication-credential-issue");
const issuedCredentials = new WeakSet<PublicationCredential>();

export interface VerificationProof {
  readonly [PROOF_BRAND]: true;
  readonly ok: true;
  readonly manifestFingerprint: string;
  readonly transport: RepresentationVerification;
  readonly decoded: RepresentationVerification;
  readonly final?: RepresentationVerification;
  readonly decodedStage: "raw" | "final";
  readonly contentCommitment: string;
  readonly transportDigest: string;
  readonly decodedDigest: string;
  readonly finalBytesCommitment: string;
  readonly byteLength: number;
  readonly revocationRefs: readonly string[];
  readonly placeholderBinding: string;
  readonly responseType: string;
  readonly requestId: string | null;
  readonly verifiedAt: number;
}

export interface CreateProofInput {
  readonly manifest: AssetManifest;
  readonly bytes: Uint8Array;
  readonly transport: RepresentationVerification;
  readonly decoded: RepresentationVerification;
  readonly final?: RepresentationVerification;
  readonly decodedStage: "raw" | "final";
  readonly contentCommitment: string;
  readonly transportDigest: string;
  readonly decodedDigest: string;
  readonly revocationRefs: Iterable<string>;
  readonly placeholderBinding?: string;
  readonly responseType?: string;
  readonly requestId?: string | null;
}

export function finalBytesCommitment(bytes: Uint8Array): string {
  return digestHex("sha256", "gsb.final-bytes.v1", bytes);
}

/**
 * Proof that both representations and every digest group were verified.  The
 * publication gate refuses any other object, so a proof cannot be confused
 * with a partially verified shard.
 */
export function createVerificationProof(input: CreateProofInput): VerificationProof {
  if (!input.transport.ok || !input.decoded.ok) {
    throw new PipelineError(
      ErrorCode.INVALID_PROOF,
      "a verification proof requires successful representation checks",
      {
        transportOk: input.transport.ok,
        decodedOk: input.decoded.ok,
      },
    );
  }
  if (input.final !== undefined && !input.final.ok) {
    throw new PipelineError(
      ErrorCode.INVALID_PROOF,
      "a verification proof requires successful final checks",
      { finalOk: input.final.ok },
    );
  }
  return Object.freeze({
    [PROOF_BRAND]: true as const,
    ok: true as const,
    manifestFingerprint: manifestFingerprint(input.manifest),
    transport: input.transport,
    decoded: input.decoded,
    ...(input.final === undefined ? {} : { final: input.final }),
    decodedStage: input.decodedStage,
    contentCommitment: input.contentCommitment,
    transportDigest: input.transportDigest,
    decodedDigest: input.decodedDigest,
    finalBytesCommitment: finalBytesCommitment(input.bytes),
    byteLength: input.bytes.byteLength,
    revocationRefs: Object.freeze([...input.revocationRefs]),
    placeholderBinding: input.placeholderBinding ?? "",
    responseType: input.responseType ?? "arraybuffer",
    requestId: input.requestId ?? null,
    verifiedAt: Date.now(),
  });
}

export function isVerificationProof(value: unknown): value is VerificationProof {
  return (
    value !== null &&
    typeof value === "object" &&
    (value as VerificationProof)[PROOF_BRAND] === true &&
    (value as VerificationProof).ok === true
  );
}

export interface PublicationMetadata {
  readonly contentCommitment: string;
  readonly transportDigest: string;
  readonly decodedDigest: string;
  readonly decodedStage: "raw" | "final";
  readonly manifestFingerprint: string;
  readonly responseType: string;
  readonly requestId: string | null;
  readonly verifiedAt: number;
  readonly byteLength: number;
}

export class PublicationCredential {
  #bytes: Uint8Array;
  #proof: VerificationProof;
  #revocations?: RevocationSet;
  #revocationRefs: readonly string[];
  #issuedAt: number;

  constructor(token: unknown, proof: VerificationProof, bytes: Uint8Array, revocations?: RevocationSet) {
    if (token !== CREDENTIAL_ISSUE || !isVerificationProof(proof)) {
      throw new PipelineError(
        ErrorCode.CREDENTIAL_INVALID,
        "publication credentials can only be issued after verification",
      );
    }
    this.#proof = proof;
    this.#bytes = cloneBytes(bytes);
    this.#revocations = revocations;
    this.#revocationRefs = Object.freeze([...proof.revocationRefs]);
    this.#issuedAt = Date.now();
    issuedCredentials.add(this);
    Object.freeze(this);
  }

  get byteLength(): number {
    return this.#bytes.byteLength;
  }

  get length(): number {
    return this.#bytes.byteLength;
  }

  get metadata(): PublicationMetadata {
    return {
      contentCommitment: this.#proof.contentCommitment,
      transportDigest: this.#proof.transportDigest,
      decodedDigest: this.#proof.decodedDigest,
      decodedStage: this.#proof.decodedStage,
      manifestFingerprint: this.#proof.manifestFingerprint,
      responseType: this.#proof.responseType,
      requestId: this.#proof.requestId,
      verifiedAt: this.#proof.verifiedAt,
      byteLength: this.#bytes.byteLength,
    };
  }

  get issuedAt(): number {
    return this.#issuedAt;
  }

  get digest(): string {
    return this.#proof.finalBytesCommitment;
  }

  assertUsable(): void {
    if (!issuedCredentials.has(this)) {
      throw new PipelineError(
        ErrorCode.CREDENTIAL_INVALID,
        "unknown publication credential",
      );
    }
    if (this.#revocations !== undefined) {
      this.#revocations.assertUsable(this.#revocationRefs);
    }
  }

  get revoked(): boolean {
    return (
      this.#revocations !== undefined &&
      this.#revocations.anyRevoked(this.#revocationRefs)
    );
  }

  read(offset = 0, length?: number): Uint8Array {
    this.assertUsable();
    if (
      !Number.isSafeInteger(offset) ||
      offset < 0 ||
      offset > this.#bytes.byteLength
    ) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "read offset is outside the published object",
        { offset, byteLength: this.#bytes.byteLength },
      );
    }
    const available = this.#bytes.byteLength - offset;
    const requested = length ?? available;
    if (!Number.isSafeInteger(requested) || requested < 0) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "read length must be a non-negative safe integer",
        { length: requested },
      );
    }
    const count = Math.min(available, requested);
    return this.#bytes.slice(offset, offset + count);
  }

  /** Returns a copy; never exposes the credential's backing buffer. */
  get bytes(): Uint8Array {
    return this.read();
  }

  slice(start = 0, end?: number): Uint8Array {
    this.assertUsable();
    return this.#bytes.slice(start, end);
  }

  checkRevocations(revocations: RevocationSet): void {
    revocations.assertUsable(this.#revocationRefs);
  }
}

function issueCredential(
  proof: VerificationProof,
  bytes: Uint8Array,
  revocations?: RevocationSet,
): PublicationCredential {
  return new PublicationCredential(CREDENTIAL_ISSUE, proof, bytes, revocations);
}

export function isPublicationCredential(
  value: unknown,
): value is PublicationCredential {
  return value instanceof PublicationCredential && issuedCredentials.has(value);
}

export type ReadCallback = (credential: PublicationCredential) => void;

export interface PublicationGateOptions {
  readonly revocations?: RevocationSet;
  readonly onCallbackError?: (error: unknown) => void;
}

type GateState = "open" | "committed" | "aborted";

/**
 * Buffers read subscribers until commit().  No callback, byte, or credential is
 * visible while the gate is open.  Abort is terminal; callbacks registered
 * after a failure never fire.
 */
export class PublicationGate {
  readonly manifest: AssetManifest;
  readonly revocations?: RevocationSet;
  readonly onCallbackError?: (error: unknown) => void;

  #state: GateState = "open";
  #callbacks = new Set<ReadCallback>();
  #credential?: PublicationCredential;
  #abortError?: PipelineError;
  #proof?: VerificationProof;

  constructor(
    manifest: AssetManifest,
    options: PublicationGateOptions = {},
  ) {
    this.manifest = manifest;
    this.revocations = options.revocations;
    this.onCallbackError = options.onCallbackError;
  }

  get state(): GateState {
    return this.#state;
  }

  get published(): boolean {
    return this.#state === "committed";
  }

  get credential(): PublicationCredential | undefined {
    return this.#credential;
  }

  onRead(callback: ReadCallback): () => void {
    if (this.#state === "aborted") {
      return () => {};
    }
    if (this.#state === "committed" && this.#credential !== undefined) {
      const credential = this.#credential;
      this.#invoke(callback, credential);
      return () => {};
    }
    this.#callbacks.add(callback);
    return () => {
      this.#callbacks.delete(callback);
    };
  }

  #invoke(callback: ReadCallback, credential: PublicationCredential): void {
    try {
      callback(credential);
    } catch (error) {
      this.onCallbackError?.(error);
    }
  }

  commit(proof: VerificationProof, bytes: Uint8Array): PublicationCredential {
    if (this.#state === "aborted") {
      throw this.#abortError ?? new PipelineError(
        ErrorCode.ABORTED,
        "publication gate was aborted",
      );
    }
    if (this.#state === "committed") {
      if (
        this.#proof !== undefined &&
        this.#proof.finalBytesCommitment === finalBytesCommitment(bytes)
      ) {
        return this.#credential as PublicationCredential;
      }
      throw new PipelineError(
        ErrorCode.CREDENTIAL_INVALID,
        "publication gate already committed a different object",
      );
    }
    if (!isVerificationProof(proof)) {
      throw new PipelineError(
        ErrorCode.INVALID_PROOF,
        "publication requires a verification proof",
      );
    }
    if (proof.manifestFingerprint !== manifestFingerprint(this.manifest)) {
      throw new PipelineError(
        ErrorCode.INVALID_PROOF,
        "verification proof belongs to a different manifest",
      );
    }
    const bytesCommitment = finalBytesCommitment(bytes);
    if (bytesCommitment !== proof.finalBytesCommitment) {
      throw new PipelineError(
        ErrorCode.INVALID_PROOF,
        "verification proof does not cover these final bytes",
        {
          expected: proof.finalBytesCommitment,
          actual: bytesCommitment,
        },
      );
    }
    if (bytes.byteLength !== proof.byteLength) {
      throw new PipelineError(
        ErrorCode.INVALID_PROOF,
        "verification proof has a different byte length",
        { expected: proof.byteLength, actual: bytes.byteLength },
      );
    }
    this.revocations?.assertUsable(proof.revocationRefs);

    this.#state = "committed";
    this.#proof = proof;
    this.#credential = issueCredential(proof, bytes, this.revocations);
    const callbacks = [...this.#callbacks];
    this.#callbacks.clear();
    for (const callback of callbacks) {
      this.#invoke(callback, this.#credential);
    }
    return this.#credential;
  }

  abort(reason?: unknown): void {
    if (this.#state === "committed") {
      throw new PipelineError(
        ErrorCode.ABORTED,
        "cannot abort an already committed publication",
      );
    }
    if (this.#state === "aborted") {
      return;
    }
    this.#state = "aborted";
    this.#abortError = asPipelineError(
      reason,
      ErrorCode.ABORTED,
      "publication aborted",
    );
    this.#callbacks.clear();
  }

  /**
   * A dropped connection may only commit an object that was already complete
   * and verified.  If the gate is open, the partial object is discarded.
   */
  disconnect(reason?: unknown): PublicationCredential | null {
    if (this.#state === "committed") {
      return this.#credential ?? null;
    }
    this.abort(reason);
    return null;
  }

  read(offset = 0, length?: number): Uint8Array {
    if (this.#state !== "committed") {
      throw new PipelineError(
        ErrorCode.NOT_PUBLISHED,
        "no bytes are published yet",
        { state: this.#state },
      );
    }
    return (this.#credential as PublicationCredential).read(offset, length);
  }
}
