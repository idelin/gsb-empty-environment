import { decodeMessage, encodeMessage, validateMessage } from "./codec-core.ts";
import type { MessageSchema } from "./schema.ts";
import { SparseVector, packDeltas, rleCompress, rleDecompress, unpackDeltas } from "./sparse.ts";

export interface DecodeOptions {
  checkRequired?: boolean;
}

/**
 * Facade over the reflection engine: schema-driven encode/decode/validate
 * plus the sparse-data optimizers, all zero-dependency.
 */
export class ProtoCodec<S extends MessageSchema = MessageSchema> {
  readonly schema: S;

  constructor(schema: S) {
    this.schema = schema;
  }

  encode(obj: Record<string, unknown>): Uint8Array {
    return encodeMessage(this.schema, obj);
  }

  decode(bytes: Uint8Array, options: DecodeOptions = {}): Record<string, unknown> {
    return decodeMessage(this.schema, bytes, options);
  }

  validate(obj: Record<string, unknown>): void {
    validateMessage(this.schema, obj);
  }
}

export { SparseVector, packDeltas, rleCompress, rleDecompress, unpackDeltas };
