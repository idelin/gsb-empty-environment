import { createHmac } from "node:crypto";

export const DANGEROUS_MIME_TYPES = [
  "text/html",
  "application/javascript",
  "image/svg+xml",
] as const;

export type DangerousMimeType = (typeof DANGEROUS_MIME_TYPES)[number];
export type SignatureEncoding = "hex" | "base64" | "base64url";
export type NotebookSecret = string | Uint8Array;

export type SignatureRegistry =
  | ReadonlySet<string>
  | readonly string[]
  | string
  | Iterable<string>;

export interface NotebookOutput {
  output_type?: string;
  data?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface NotebookCell {
  cell_type?: string;
  source?: unknown;
  outputs?: NotebookOutput[];
  metadata?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface Notebook {
  cells?: NotebookCell[];
  metadata?: Record<string, unknown>;
  [key: string]: unknown;
}

export type NotebookInput = Notebook | string;

export interface TrustOptions {
  secret: NotebookSecret;
  signatures?: SignatureRegistry;
  registeredSignatures?: SignatureRegistry;
  trustedSignatures?: SignatureRegistry;
}

export interface SanitizeResult extends Notebook {
  notebook: Notebook;
  sanitized: boolean;
  removedOutputs: number;
  removedMimeTypes: DangerousMimeType[];
}

export interface OpenNotebookResult extends Notebook {
  notebook: Notebook;
  trusted: boolean;
  registered: boolean;
  sanitized: boolean;
  signature: string;
  removedOutputs: number;
  removedMimeTypes: DangerousMimeType[];
}

export interface SaveNotebookResult extends Notebook {
  notebook: Notebook;
  trusted: true;
  signature: string;
  signatures: Set<string>;
  registeredSignatures: Set<string>;
}

const DANGEROUS_MIME_SET = new Set<string>(DANGEROUS_MIME_TYPES);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function defineOwn(
  target: Record<string, unknown>,
  key: string,
  value: unknown,
): void {
  Object.defineProperty(target, key, {
    configurable: true,
    enumerable: true,
    value,
    writable: true,
  });
}

function parseNotebookInput(notebook: NotebookInput): Notebook {
  if (typeof notebook !== "string") {
    return notebook;
  }

  const parsed: unknown = JSON.parse(notebook);
  if (!isRecord(parsed)) {
    throw new TypeError("Notebook JSON must contain an object");
  }
  return parsed;
}

/**
 * JSON-compatible stable serializer. Object keys are sorted at every level and
 * no insignificant whitespace is emitted.
 */
export function canonicalStringify(value: unknown): string {
  const ancestors = new Set<object>();

  const serialize = (input: unknown): string | undefined => {
    if (
      input !== null &&
      typeof input === "object" &&
      typeof (input as { toJSON?: unknown }).toJSON === "function"
    ) {
      input = (input as { toJSON: () => unknown }).toJSON();
    }

    if (input === null) {
      return "null";
    }

    switch (typeof input) {
      case "string":
      case "boolean":
        return JSON.stringify(input);
      case "number":
        return Number.isFinite(input) ? JSON.stringify(input) : "null";
      case "bigint":
        throw new TypeError("BigInt values are not valid JSON");
      case "undefined":
      case "function":
      case "symbol":
        return undefined;
      default:
        break;
    }

    if (ancestors.has(input)) {
      throw new TypeError("Cannot canonicalize a circular structure");
    }

    ancestors.add(input);
    try {
      if (Array.isArray(input)) {
        const items: string[] = [];
        for (let index = 0; index < input.length; index += 1) {
          items.push(serialize(input[index]) ?? "null");
        }
        return `[${items.join(",")}]`;
      }

      const object = input as Record<string, unknown>;
      const pairs: string[] = [];
      for (const key of Object.keys(object).sort()) {
        const serialized = serialize(object[key]);
        if (serialized !== undefined) {
          pairs.push(`${JSON.stringify(key)}:${serialized}`);
        }
      }
      return `{${pairs.join(",")}}`;
    } finally {
      ancestors.delete(input);
    }
  };

  return serialize(value) ?? "null";
}

/**
 * Canonical JSON used as the HMAC payload. The notebook-level
 * metadata.signature field is deliberately removed so an imported signature
 * never contributes to the value that is recomputed.
 */
export function canonicalizeNotebook(notebook: NotebookInput): string {
  const value = parseNotebookInput(notebook);
  const root: Record<string, unknown> = {};

  for (const key of Object.keys(value)) {
    if (key === "metadata" && isRecord(value.metadata)) {
      const metadata: Record<string, unknown> = {};
      for (const metadataKey of Object.keys(value.metadata)) {
        if (metadataKey !== "signature") {
          defineOwn(metadata, metadataKey, value.metadata[metadataKey]);
        }
      }
      root.metadata = metadata;
    } else {
      defineOwn(root, key, value[key]);
    }
  }

  return canonicalStringify(root);
}

export function computeNotebookSignature(
  notebook: NotebookInput,
  secret: NotebookSecret,
  encoding: SignatureEncoding = "hex",
): string {
  return createHmac("sha256", secret)
    .update(canonicalizeNotebook(notebook))
    .digest(encoding);
}

function normalizeSignature(signature: string): string {
  const trimmed = signature.trim();
  return /^[0-9a-fA-F]{64}$/.test(trimmed) ? trimmed.toLowerCase() : trimmed;
}

function signatureCollection(registry?: SignatureRegistry): Set<string> {
  if (registry === undefined) {
    return new Set();
  }
  if (registry instanceof Set) {
    return registry as Set<string>;
  }
  if (typeof registry === "string") {
    return new Set([registry]);
  }
  if (Array.isArray(registry)) {
    return new Set(registry);
  }
  return new Set(registry as Iterable<string>);
}

function registerSignatureInCollection(
  registry: SignatureRegistry | undefined,
  signature: string,
): Set<string> {
  if (registry instanceof Set) {
    registry.add(signature);
    return registry as Set<string>;
  }

  if (Array.isArray(registry)) {
    if (
      !Object.isFrozen(registry) &&
      Object.isExtensible(registry) &&
      !registry.includes(signature)
    ) {
      (registry as string[]).push(signature);
    }
    const signatures = new Set(registry);
    signatures.add(signature);
    return signatures;
  }

  const signatures = signatureCollection(registry);
  signatures.add(signature);
  return signatures;
}

function hasSignature(
  registry: SignatureRegistry | undefined,
  signature: string,
): boolean {
  if (registry === undefined) {
    return false;
  }

  const expected = normalizeSignature(signature);
  for (const candidate of signatureCollection(registry)) {
    if (normalizeSignature(candidate) === expected) {
      return true;
    }
  }
  return false;
}

function resolveTrustArguments(
  secretOrOptions: NotebookSecret | TrustOptions,
  signatures?: SignatureRegistry,
): { secret: NotebookSecret; signatures?: SignatureRegistry } {
  if (
    isRecord(secretOrOptions) &&
    !ArrayBuffer.isView(secretOrOptions) &&
    ("secret" in secretOrOptions ||
      "signatures" in secretOrOptions ||
      "registeredSignatures" in secretOrOptions ||
      "trustedSignatures" in secretOrOptions)
  ) {
    const options = secretOrOptions as TrustOptions;
    if (
      options.secret === undefined ||
      (!ArrayBuffer.isView(options.secret) &&
        typeof options.secret !== "string")
    ) {
      throw new TypeError("A server secret is required");
    }
    return {
      secret: options.secret,
      signatures:
        options.signatures ??
        options.registeredSignatures ??
        options.trustedSignatures,
    };
  }

  return {
    secret: secretOrOptions as NotebookSecret,
    signatures,
  };
}

function cloneJson<T>(value: T): T {
  if (Array.isArray(value)) {
    return value.map((item) => cloneJson(item)) as T;
  }
  if (!isRecord(value)) {
    return value;
  }

  const output: Record<string, unknown> = {};
  for (const key of Object.keys(value)) {
    defineOwn(output, key, cloneJson(value[key]));
  }
  return output as T;
}

function normalizedMimeType(mimeType: string): string {
  return mimeType.split(";", 1)[0].trim().toLowerCase();
}

/**
 * Removes dangerous MIME representations from untrusted output. Outputs that
 * contain only dangerous MIME types are removed entirely; mixed outputs keep
 * their safe representations. The output-level metadata.trusted flag is never
 * trusted and is removed from the returned data.
 */
export function sanitizeNotebookOutputs(
  notebook: NotebookInput,
): SanitizeResult {
  const source = cloneJson(parseNotebookInput(notebook));
  const removedMimeTypes = new Set<DangerousMimeType>();
  let removedOutputs = 0;

  if (Array.isArray(source.cells)) {
    for (const cell of source.cells) {
      if (!isRecord(cell) || !Array.isArray(cell.outputs)) {
        continue;
      }

      const keptOutputs: NotebookOutput[] = [];
      for (const output of cell.outputs) {
        if (!isRecord(output)) {
          keptOutputs.push(output);
          continue;
        }

        let removedDangerousData = false;
        if (isRecord(output.data)) {
          const safeData: Record<string, unknown> = {};
          for (const key of Object.keys(output.data)) {
            const mime = normalizedMimeType(key);
            if (DANGEROUS_MIME_SET.has(mime)) {
              removedMimeTypes.add(mime as DangerousMimeType);
              removedDangerousData = true;
            } else {
              defineOwn(safeData, key, output.data[key]);
            }
          }
          output.data = safeData;
        }

        if (
          removedDangerousData &&
          isRecord(output.data) &&
          Object.keys(output.data).length === 0
        ) {
          removedOutputs += 1;
          continue;
        }

        if (isRecord(output.metadata)) {
          const metadata: Record<string, unknown> = {};
          for (const key of Object.keys(output.metadata)) {
            if (key !== "trusted") {
              defineOwn(metadata, key, output.metadata[key]);
            }
          }
          if (Object.keys(metadata).length === 0) {
            delete output.metadata;
          } else {
            output.metadata = metadata;
          }
        }

        keptOutputs.push(output);
      }
      cell.outputs = keptOutputs;
    }
  }

  const removed = [...removedMimeTypes].sort() as DangerousMimeType[];
  return {
    ...source,
    notebook: source,
    sanitized: removedOutputs > 0 || removed.length > 0,
    removedOutputs,
    removedMimeTypes: removed,
  };
}

export function isNotebookTrusted(
  notebook: NotebookInput,
  secretOrOptions: NotebookSecret | TrustOptions,
  signatures?: SignatureRegistry,
): boolean {
  const args = resolveTrustArguments(secretOrOptions, signatures);
  const signature = computeNotebookSignature(notebook, args.secret);
  return hasSignature(args.signatures, signature);
}

export function verifyNotebookSignature(
  notebook: NotebookInput,
  secret: NotebookSecret,
  expectedSignature: string,
): boolean {
  return normalizeSignature(computeNotebookSignature(notebook, secret)) ===
    normalizeSignature(expectedSignature);
}

/**
 * Recomputes the notebook signature and consults only the registered signature
 * collection. The signature embedded in the file and every output metadata
 * trusted flag are ignored.
 */
export function openNotebook(
  notebook: NotebookInput,
  secretOrOptions: NotebookSecret | TrustOptions,
  signatures?: SignatureRegistry,
): OpenNotebookResult {
  const args = resolveTrustArguments(secretOrOptions, signatures);
  const signature = computeNotebookSignature(notebook, args.secret);
  const trusted = hasSignature(args.signatures, signature);

  let projected: Notebook;
  let removedOutputs = 0;
  let removedMimeTypes: DangerousMimeType[] = [];

  if (trusted) {
    projected = cloneJson(parseNotebookInput(notebook));
  } else {
    const sanitized = sanitizeNotebookOutputs(notebook);
    projected = sanitized.notebook;
    removedOutputs = sanitized.removedOutputs;
    removedMimeTypes = sanitized.removedMimeTypes;
  }

  return {
    ...projected,
    notebook: projected,
    trusted,
    registered: trusted,
    sanitized: !trusted,
    signature,
    removedOutputs,
    removedMimeTypes,
  };
}

/**
 * Explicit save after a user execution. The signature is recomputed from the
 * submitted notebook (never copied from file metadata) and added to the
 * registry. A mutable Set registry is updated in place.
 */
export function saveNotebook(
  notebook: NotebookInput,
  secretOrOptions: NotebookSecret | TrustOptions,
  signatures?: SignatureRegistry,
): SaveNotebookResult {
  const args = resolveTrustArguments(secretOrOptions, signatures);
  const signature = computeNotebookSignature(notebook, args.secret);
  const signed = cloneJson(parseNotebookInput(notebook));

  const metadata = isRecord(signed.metadata)
    ? cloneJson(signed.metadata)
    : {};
  metadata.signature = signature;
  signed.metadata = metadata;

  const registry = registerSignatureInCollection(args.signatures, signature);

  return {
    ...signed,
    notebook: signed,
    trusted: true,
    signature,
    signatures: registry,
    registeredSignatures: registry,
  };
}

export class NotebookTrustService {
  #secret: NotebookSecret;
  #signatures: Set<string>;

  constructor(
    secret: NotebookSecret,
    signatures?: SignatureRegistry,
  ) {
    this.#secret = secret;
    this.#signatures = signatureCollection(signatures);
  }

  get registeredSignatures(): Set<string> {
    return this.#signatures;
  }

  canonicalize(notebook: NotebookInput): string {
    return canonicalizeNotebook(notebook);
  }

  sign(notebook: NotebookInput, encoding: SignatureEncoding = "hex"): string {
    return computeNotebookSignature(notebook, this.#secret, encoding);
  }

  isTrusted(notebook: NotebookInput): boolean {
    const signature = this.sign(notebook);
    return hasSignature(this.#signatures, signature);
  }

  open(notebook: NotebookInput): OpenNotebookResult {
    return openNotebook(notebook, this.#secret, this.#signatures);
  }

  save(notebook: NotebookInput): SaveNotebookResult {
    return saveNotebook(notebook, this.#secret, this.#signatures);
  }

  registerSignature(signature: string): void {
    this.#signatures.add(normalizeSignature(signature));
  }
}

export function createNotebookTrustService(
  secret: NotebookSecret,
  signatures?: SignatureRegistry,
): NotebookTrustService {
  return new NotebookTrustService(secret, signatures);
}

export const canonicalNotebookJSON = canonicalizeNotebook;
export const stableStringify = canonicalStringify;
export const signNotebook = computeNotebookSignature;
export const computeSignature = computeNotebookSignature;
export const isTrustedNotebook = isNotebookTrusted;
export const sanitizeNotebook = sanitizeNotebookOutputs;
export const loadNotebook = openNotebook;
export const saveAndRegisterNotebook = saveNotebook;
export const registerNotebookSignature = saveNotebook;
export const createTrustService = createNotebookTrustService;

const notebookTrust = {
  DANGEROUS_MIME_TYPES,
  NotebookTrustService,
  canonicalizeNotebook,
  canonicalStringify,
  computeNotebookSignature,
  createNotebookTrustService,
  isNotebookTrusted,
  openNotebook,
  sanitizeNotebookOutputs,
  saveNotebook,
  verifyNotebookSignature,
};

export default notebookTrust;
