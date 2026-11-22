import { ErrorCode, PipelineError } from "./errors.ts";

function bytesToBase64(bytes: Uint8Array): string {
  return Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength).toString(
    "base64",
  );
}

/**
 * Deterministic JSON used for cache keys and request-identity bindings.
 * Object keys are sorted, typed arrays are tagged, and unsupported values are
 * rejected rather than silently producing collisions.
 */
export function canonicalJson(value: unknown): string {
  const seen = new WeakSet<object>();

  const visit = (input: unknown, path: string): string => {
    if (input === null) {
      return "null";
    }
    switch (typeof input) {
      case "string":
        return JSON.stringify(input);
      case "boolean":
        return input ? "true" : "false";
      case "number":
        if (!Number.isFinite(input)) {
          throw new PipelineError(
            ErrorCode.INVALID_ARGUMENT,
            `non-finite number at ${path}`,
            { path, value: String(input) },
          );
        }
        return Object.is(input, -0) ? "0" : String(input);
      case "bigint":
        return `{"$bigint":${JSON.stringify(input.toString())}}`;
      case "undefined":
        return "null";
      case "function":
      case "symbol":
        throw new PipelineError(
          ErrorCode.INVALID_ARGUMENT,
          `unsupported ${typeof input} at ${path}`,
          { path },
        );
      case "object":
        break;
      default:
        throw new PipelineError(
          ErrorCode.INVALID_ARGUMENT,
          `unsupported value at ${path}`,
          { path },
        );
    }

    const object = input as object;
    if (seen.has(object)) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        `cyclic value at ${path}`,
        { path },
      );
    }
    seen.add(object);

    try {
      if (input instanceof Uint8Array) {
        return `{"$bytes":${JSON.stringify(bytesToBase64(input))}}`;
      }
      if (input instanceof ArrayBuffer) {
        return `{"$bytes":${JSON.stringify(
          Buffer.from(input).toString("base64"),
        )}}`;
      }
      if (Array.isArray(input)) {
        return `[${input
          .map((item, index) => visit(item, `${path}[${index}]`))
          .join(",")}]`;
      }
      if (input instanceof Map) {
        const entries = [...input.entries()].map(([key, entryValue]) => [
          String(key),
          entryValue,
        ] as const);
        entries.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
        return `{${entries
          .map(
            ([key, entryValue]) =>
              `${JSON.stringify(key)}:${visit(
                entryValue,
                `${path}.${key}`,
              )}`,
          )
          .join(",")}}`;
      }
      if (input instanceof Set) {
        return `{"$set":[${[...input]
          .map((item, index) => visit(item, `${path}[${index}]`))
          .join(",")}]}`;
      }
      const record = input as Record<string, unknown>;
      const keys = Object.keys(record).sort();
      return `{${keys
        .filter((key) => record[key] !== undefined)
        .map(
          (key) =>
            `${JSON.stringify(key)}:${visit(record[key], `${path}.${key}`)}`,
        )
        .join(",")}}`;
    } finally {
      seen.delete(object);
    }
  };

  return visit(value, "$");
}

export function cloneBytes(bytes: Uint8Array): Uint8Array {
  return Uint8Array.from(bytes);
}

export function bytesEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.byteLength !== right.byteLength) {
    return false;
  }
  for (let index = 0; index < left.byteLength; index += 1) {
    if (left[index] !== right[index]) {
      return false;
    }
  }
  return true;
}

export function concatBytes(chunks: readonly Uint8Array[]): Uint8Array {
  let total = 0;
  for (const chunk of chunks) {
    total += chunk.byteLength;
  }
  const output = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return output;
}

export function encodeText(value: string): Uint8Array {
  return new Uint8Array(Buffer.from(value, "utf8"));
}

export function decodeText(value: Uint8Array): string {
  return Buffer.from(value.buffer, value.byteOffset, value.byteLength).toString(
    "utf8",
  );
}
