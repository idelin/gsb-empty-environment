export type ClassDef =
  | { readonly kind: 'scalar'; readonly byteLength: number }
  | { readonly kind: 'collection' }
  | { readonly kind: 'reference' };

export type Registry = ReadonlyMap<number, ClassDef>;

export interface PreflightOptions {
  readonly maxCollectionLength?: number;
  readonly maxDepth?: number;
  readonly maxVarintBytes?: number;
}

export type PreflightErrorCode =
  | 'CLASS_NAME_NOT_ALLOWED'
  | 'UNREGISTERED_CLASS'
  | 'DANGLING_REFERENCE'
  | 'VARINT_TOO_LONG'
  | 'TRUNCATED_VARINT'
  | 'TRUNCATED_PAYLOAD'
  | 'COLLECTION_TOO_LARGE'
  | 'DEPTH_EXCEEDED';

export class PreflightError extends Error {
  readonly code: PreflightErrorCode;
  readonly offset: number;

  constructor(code: PreflightErrorCode, offset: number, message: string) {
    super(message);
    this.name = 'PreflightError';
    this.code = code;
    this.offset = offset;
  }
}

export interface PreflightStats {
  readonly objects: number;
  readonly references: number;
  readonly nulls: number;
  readonly bytesRead: number;
}

export type PreflightResult =
  | { readonly ok: true; readonly stats: PreflightStats }
  | { readonly ok: false; readonly error: PreflightError };

const DEFAULT_MAX_COLLECTION_LENGTH = 1024;
const DEFAULT_MAX_DEPTH = 64;
const DEFAULT_MAX_VARINT_BYTES = 5;

export function preflight(
  data: Uint8Array,
  registry: Registry,
  options: PreflightOptions = {},
): PreflightResult {
  const maxCollectionLength = options.maxCollectionLength ?? DEFAULT_MAX_COLLECTION_LENGTH;
  const maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
  const maxVarintBytes = options.maxVarintBytes ?? DEFAULT_MAX_VARINT_BYTES;

  for (const [registrationId, def] of registry) {
    if (!Number.isSafeInteger(registrationId) || registrationId < 0) {
      throw new TypeError(`invalid registration id: ${registrationId}`);
    }
    if (def.kind === 'scalar' && (!Number.isSafeInteger(def.byteLength) || def.byteLength < 0)) {
      throw new TypeError(`invalid byteLength for registration id ${registrationId}`);
    }
  }

  let pos = 0;
  let objects = 0;
  let references = 0;
  let nulls = 0;

  function readVarint(): number {
    const start = pos;
    let value = 0;
    for (let i = 0; i < maxVarintBytes; i++) {
      if (pos >= data.length) {
        throw new PreflightError('TRUNCATED_VARINT', start, `varint starting at offset ${start} is truncated`);
      }
      const byte = data[pos++];
      value += (byte & 0x7f) * 2 ** (7 * i);
      if ((byte & 0x80) === 0) return value;
    }
    throw new PreflightError('VARINT_TOO_LONG', start, `varint at offset ${start} exceeds ${maxVarintBytes} bytes`);
  }

  function readNode(depth: number): void {
    const tagOffset = pos;
    const tag = readVarint();
    if (tag === 0) {
      nulls++;
      return;
    }
    if (tag === 1) {
      throw new PreflightError('CLASS_NAME_NOT_ALLOWED', tagOffset, 'class-name form is forbidden; only registered class ids are accepted');
    }
    const registrationId = tag - 2;
    const def = registry.get(registrationId);
    if (def === undefined) {
      throw new PreflightError('UNREGISTERED_CLASS', tagOffset, `registration id ${registrationId} is not in the registry`);
    }
    switch (def.kind) {
      case 'scalar': {
        if (data.length - pos < def.byteLength) {
          throw new PreflightError('TRUNCATED_PAYLOAD', pos, `scalar payload of ${def.byteLength} byte(s) is truncated at offset ${pos}`);
        }
        pos += def.byteLength;
        break;
      }
      case 'collection': {
        const lengthOffset = pos;
        const length = readVarint();
        if (length > maxCollectionLength) {
          throw new PreflightError('COLLECTION_TOO_LARGE', lengthOffset, `collection length ${length} exceeds budget ${maxCollectionLength}`);
        }
        if (depth >= maxDepth) {
          throw new PreflightError('DEPTH_EXCEEDED', tagOffset, `nesting depth exceeds budget ${maxDepth}`);
        }
        for (let i = 0; i < length; i++) readNode(depth + 1);
        break;
      }
      case 'reference': {
        const indexOffset = pos;
        const index = readVarint();
        if (index >= objects) {
          throw new PreflightError('DANGLING_REFERENCE', indexOffset, `reference points to object #${index} but only ${objects} object(s) seen so far`);
        }
        references++;
        return;
      }
    }
    objects++;
  }

  try {
    while (pos < data.length) readNode(0);
    return { ok: true, stats: { objects, references, nulls, bytesRead: pos } };
  } catch (err) {
    if (err instanceof PreflightError) return { ok: false, error: err };
    throw err;
  }
}

export function assertPreflight(data: Uint8Array, registry: Registry, options: PreflightOptions = {}): PreflightStats {
  const result = preflight(data, registry, options);
  if (!result.ok) throw result.error;
  return result.stats;
}
