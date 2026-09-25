import { createHmac } from "node:crypto";
import { canonicalize } from "./canonicalize.ts";
import type { JsonValue } from "./types.ts";

export const DEFAULT_PSEUDONYM_LENGTH = 24;

export class Pseudonymizer {
  readonly #key: string | Uint8Array;
  readonly #length: number;

  constructor(key: string | Uint8Array, length: number = DEFAULT_PSEUDONYM_LENGTH) {
    if (length < 4 || length > 64) {
      throw new Error("pseudonymLength must be between 4 and 64 hex chars");
    }
    this.#key = key;
    this.#length = length;
  }

  digest(domain: string, value: JsonValue): string {
    return createHmac("sha256", this.#key)
      .update("v1\x00" + domain + "\x00" + canonicalize(value))
      .digest("hex");
  }

  pseudonym(domain: string, value: JsonValue): string {
    return `ps_${this.digest(domain, value).slice(0, this.#length)}`;
  }
}
