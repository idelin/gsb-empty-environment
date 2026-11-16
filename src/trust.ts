import { createHmac, timingSafeEqual } from "node:crypto";

/**
 * MIME types whose outputs may execute markup or script and therefore must be
 * stripped from any notebook that fails the signature check.
 */
export const DANGEROUS_MIME_TYPES = [
  "text/html",
  "application/javascript",
  "image/svg+xml",
] as const;

/** Output metadata key that attackers can forge; it never confers trust. */
export const TRUSTED_METADATA_KEY = "trusted";

/** Location of the signature embedded in a notebook document. */
const SIGNATURE_PATH = ["metadata", "signature"] as const;

export type MimeBundle = Record<string, unknown>;

export interface NotebookOutput {
  output_type?: string;
  data?: MimeBundle;
  metadata?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface NotebookCell {
  cell_type?: string;
  source?: unknown;
  outputs?: NotebookOutput[];
  [key: string]: unknown;
}

export interface Notebook {
  metadata?: Record<string, unknown>;
  cells?: NotebookCell[];
  [key: string]: unknown;
}

export interface OpenResult {
  /** True only when a recomputed signature is present in the registered set. */
  trusted: boolean;
  /** Deep-cloned notebook; dangerous outputs are removed when not trusted. */
  notebook: Notebook;
  /** Signature recomputed from the notebook contents on open. */
  signature: string;
}

export interface SaveResult {
  /** Freshly recomputed signature that has been added to the registry. */
  signature: string;
  /** Deep-cloned notebook; an embedded signature field is never registered. */
  notebook: Notebook;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Remove metadata.signature from a deep clone. The embedded signature is part
 * of the file format but must never feed the canonical form or the registry.
 */
function stripSignature(notebook: Notebook): Notebook {
  const clone = structuredClone(notebook) as Notebook;
  if (isRecord(clone.metadata)) {
    delete clone.metadata[SIGNATURE_PATH[1]];
  }
  return clone;
}

/**
 * Recursively sort object keys in lexicographic order. Arrays keep their order
 * because element order is meaningful notebook content.
 */
function sortKeysDeep(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(sortKeysDeep);
  }
  if (isRecord(value)) {
    const sorted: Record<string, unknown> = {};
    for (const key of Object.keys(value).sort()) {
      sorted[key] = sortKeysDeep(value[key]);
    }
    return sorted;
  }
  return value;
}

/**
 * Canonical JSON used as the HMAC payload: metadata.signature removed, object
 * keys sorted lexicographically at every depth, no insignificant whitespace.
 * The payload covers every cell, including all cell outputs and their
 * metadata (the forgeable `trusted` flag included).
 */
export function canonicalizeNotebook(notebook: Notebook): string {
  const withoutSignature = stripSignature(notebook);
  const sorted = sortKeysDeep(withoutSignature);
  return JSON.stringify(sorted);
}

/**
 * Compute the HMAC-SHA256 signature of the canonical notebook. The signature
 * is computed over all cells and outputs; changing any of them changes it.
 */
export function computeSignature(notebook: Notebook, serverKey: string): string {
  if (typeof serverKey !== "string" || serverKey.length === 0) {
    throw new Error("A non-empty server key is required to sign notebooks.");
  }
  return createHmac("sha256", serverKey)
    .update(canonicalizeNotebook(notebook), "utf8")
    .digest("hex");
}

function signatureMatches(registered: string, computed: string): boolean {
  const a = Buffer.from(registered.toLowerCase(), "hex");
  const b = Buffer.from(computed.toLowerCase(), "hex");
  if (a.length !== b.length) {
    return false;
  }
  return timingSafeEqual(a, b);
}

/**
 * Decide whether a notebook is trusted by recomputing its signature and
 * looking the result up in the server-side registered signature set.
 * Output metadata `trusted` and any embedded signature are ignored here.
 */
export function isNotebookTrusted(
  notebook: Notebook,
  serverKey: string,
  registeredSignatures: ReadonlySet<string>,
): boolean {
  const computed = computeSignature(notebook, serverKey);
  for (const registered of registeredSignatures) {
    if (signatureMatches(registered, computed)) {
      return true;
    }
  }
  return false;
}

/**
 * Remove outputs that can run markup/script and the forgeable output metadata
 * `trusted` flag. Rich outputs left without any MIME bundle are dropped.
 */
export function sanitizeUntrustedNotebook(notebook: Notebook): Notebook {
  const clone = structuredClone(notebook) as Notebook;
  const dangerous = new Set<string>(DANGEROUS_MIME_TYPES);

  for (const cell of clone.cells ?? []) {
    if (!Array.isArray(cell.outputs)) {
      continue;
    }

    const keptOutputs: NotebookOutput[] = [];
    for (const output of cell.outputs) {
      if (isRecord(output.metadata)) {
        delete output.metadata[TRUSTED_METADATA_KEY];
      }

      if (isRecord(output.data)) {
        for (const mimeType of Object.keys(output.data)) {
          if (dangerous.has(mimeType)) {
            delete output.data[mimeType];
          }
        }
        if (Object.keys(output.data).length === 0) {
          continue;
        }
      }

      keptOutputs.push(output);
    }
    cell.outputs = keptOutputs;
  }

  return clone;
}

/**
 * Open a notebook received from another user. The signature is always
 * recomputed; trust requires membership in the registered set. Untrusted
 * notebooks are sanitized. The embedded metadata.signature is preserved on
 * the returned document but never used for trust or registration.
 */
export function openNotebook(
  notebook: Notebook,
  serverKey: string,
  registeredSignatures: ReadonlySet<string>,
): OpenResult {
  const signature = computeSignature(notebook, serverKey);
  const trusted = isNotebookTrusted(notebook, serverKey, registeredSignatures);
  const safeNotebook = trusted
    ? (structuredClone(notebook) as Notebook)
    : sanitizeUntrustedNotebook(notebook);

  return { trusted, notebook: safeNotebook, signature };
}

/**
 * Register a signature after the user has executed and saved a notebook in
 * the UI. The signature is always recomputed from current contents, so a
 * signature field shipped inside the imported file is never registered.
 */
export function registerSignature(
  notebook: Notebook,
  serverKey: string,
  registeredSignatures: Set<string>,
): string {
  const signature = computeSignature(notebook, serverKey);
  registeredSignatures.add(signature);
  return signature;
}

/**
 * Save flow for a notebook executed in the UI: recompute the signature over
 * the current contents and register it. The returned notebook is a clone and
 * no signature is written into its metadata by this function.
 */
export function saveNotebook(
  notebook: Notebook,
  serverKey: string,
  registeredSignatures: Set<string>,
): SaveResult {
  const signature = registerSignature(notebook, serverKey, registeredSignatures);
  return { signature, notebook: structuredClone(notebook) as Notebook };
}
