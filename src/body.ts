import { isBytes32, keccak256Hex, utf8Bytes } from "./hash.ts";

export type BodyInput =
  | string
  | Uint8Array
  | ArrayBuffer
  | Record<string, unknown>
  | readonly unknown[];

function isArrayBuffer(value: unknown): value is ArrayBuffer {
  return (
    value instanceof ArrayBuffer ||
    (typeof SharedArrayBuffer !== "undefined" &&
      value instanceof SharedArrayBuffer)
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function stableJson(value: unknown): string | undefined {
  if (value === null) {
    return "null";
  }
  if (value === undefined) {
    return undefined;
  }
  if (typeof value === "string") {
    return JSON.stringify(value);
  }
  if (typeof value === "boolean") {
    return value ? "true" : "false";
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new TypeError("Cannot hash a non-finite JSON number");
    }
    return JSON.stringify(value);
  }
  if (typeof value === "bigint") {
    throw new TypeError(
      "Cannot hash a bigint in a JSON body; pass the raw wire body instead",
    );
  }
  if (Array.isArray(value)) {
    return `[${value
      .map((entry) => stableJson(entry) ?? "null")
      .join(",")}]`;
  }
  if (isRecord(value)) {
    const entries = Object.keys(value)
      .sort()
      .flatMap((key) => {
        const encoded = stableJson(value[key]);
        return encoded === undefined
          ? []
          : [`${JSON.stringify(key)}:${encoded}`];
      });
    return `{${entries.join(",")}}`;
  }
  throw new TypeError("Unsupported value in JSON body");
}

export function stableStringify(value: unknown): string {
  const encoded = stableJson(value);
  if (encoded === undefined) {
    throw new TypeError("Cannot stringify undefined");
  }
  return encoded;
}

export function bodyToBytes(body: BodyInput): Uint8Array {
  if (typeof body === "string") {
    return utf8Bytes(body);
  }
  if (body instanceof Uint8Array) {
    return body;
  }
  if (isArrayBuffer(body)) {
    return new Uint8Array(body);
  }
  if (typeof body === "object" && body !== null) {
    return utf8Bytes(stableStringify(body));
  }
  throw new TypeError("Body must be raw bytes, a JSON string, or a JSON value");
}

export function hashBody(body: BodyInput): string {
  return keccak256Hex(bodyToBytes(body));
}

export function parseBody(body: BodyInput): unknown {
  if (typeof body === "string") {
    return JSON.parse(body) as unknown;
  }
  if (body instanceof Uint8Array || isArrayBuffer(body)) {
    const text = new TextDecoder("utf-8", { fatal: true }).decode(
      body instanceof Uint8Array ? body : new Uint8Array(body),
    );
    return JSON.parse(text) as unknown;
  }
  return body;
}

export function isResponseObject(
  value: unknown,
): value is Record<string, unknown> {
  return isRecord(value);
}

export function isDeploymentId(value: unknown): value is string {
  return isBytes32(value);
}
