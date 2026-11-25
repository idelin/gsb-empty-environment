import type { KeyId } from "./types.ts";
import type { KeyObject } from "node:crypto";

export class MapKeyDirectory {
  private readonly keys = new Map<KeyId, KeyObject>();

  add(keyId: KeyId, publicKey: KeyObject): this {
    this.keys.set(keyId, publicKey);
    return this;
  }

  getPublicKey(keyId: KeyId): KeyObject | undefined {
    return this.keys.get(keyId);
  }
}
