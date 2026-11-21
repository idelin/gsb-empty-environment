import {
  createCipheriv,
  createDecipheriv,
  createHash,
  randomBytes as nodeRandomBytes,
  scryptSync,
} from "node:crypto";
import { AuditDecryptionError, DesensitizationError } from "./errors.ts";
import type {
  AuditEntry,
  AuditReason,
  MaskingStrategy,
  SensitivityLevel,
} from "./types.ts";

const AUDIT_AAD = Buffer.from("ps-desensitizer-audit-v1");
const TOKEN_PATTERN =
  /\{\{[^{}]+\}\}|\[\[[^\[\]]+\]\]|<<[^<>]+>>|«[^»]+»|⟦[^⟧]+⟧|\u0001[^\u0002]+\u0002/g;

export function deepClone<T>(value: T): T {
  if (value === null || typeof value !== "object") {
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((item) => deepClone(item)) as T;
  }
  const output: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(
    value as Record<string, unknown>,
  )) {
    Object.defineProperty(output, key, {
      value: deepClone(item),
      enumerable: true,
      writable: true,
      configurable: true,
    });
  }
  return output as T;
}

function valueToText(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === undefined) return "";
  const serialized = JSON.stringify(value);
  return serialized === undefined ? String(value) : serialized;
}

export interface RecordAuditInput {
  token: string;
  path: string;
  level: SensitivityLevel;
  reason: AuditReason;
  original: unknown;
  sensitiveValue?: string;
  encoding?: string;
  strategy: MaskingStrategy;
  field?: string;
  createdAt?: number;
}

export class AuditVault {
  private readonly byToken = new Map<string, AuditEntry>();
  private readonly ordered: AuditEntry[] = [];

  get size(): number {
    return this.ordered.length;
  }

  has(token: string): boolean {
    return this.byToken.has(token);
  }

  record(input: RecordAuditInput): AuditEntry {
    if (this.byToken.has(input.token)) {
      throw new DesensitizationError(
        `Duplicate audit token generated: ${input.token}`,
      );
    }
    const entry: AuditEntry = {
      token: input.token,
      path: input.path,
      level: input.level,
      reason: input.reason,
      original: deepClone(input.original),
      sensitiveValue: input.sensitiveValue,
      encoding: input.encoding,
      strategy: input.strategy,
      field: input.field,
      createdAt: input.createdAt ?? Date.now(),
    };
    this.byToken.set(entry.token, entry);
    this.ordered.push(entry);
    return entry;
  }

  get(token: string): AuditEntry | undefined {
    const entry = this.byToken.get(token);
    return entry ? this.cloneEntry(entry) : undefined;
  }

  reveal(token: string): unknown {
    const entry = this.byToken.get(token);
    if (!entry) {
      throw new DesensitizationError(`Unknown audit token: ${token}`);
    }
    return deepClone(entry.original);
  }

  entries(): AuditEntry[] {
    return this.entriesFrom(0);
  }

  entriesFrom(start: number): AuditEntry[] {
    return this.ordered
      .slice(Math.max(0, start))
      .map((entry) => this.cloneEntry(entry));
  }

  toJSON(): AuditEntry[] {
    return this.entries();
  }

  clear(): void {
    this.byToken.clear();
    this.ordered.length = 0;
  }

  merge(other: AuditVault): void {
    for (const entry of other.entries()) {
      this.record(entry);
    }
  }

  replaceTokens(
    value: string,
    replacer?: (token: string, entry: AuditEntry) => string,
  ): string {
    if (this.byToken.size === 0) {
      return value;
    }
    return value.replace(TOKEN_PATTERN, (token) => {
      const entry = this.byToken.get(token);
      if (!entry) return token;
      if (replacer) return replacer(token, entry);
      return valueToText(entry.original);
    });
  }

  /**
   * Export the reversible mapping. Without a passphrase this is plaintext and
   * must be protected by the caller. With a passphrase the mapping is
   * encrypted with AES-256-GCM and scrypt key derivation.
   */
  export(passphrase?: string | Uint8Array): string {
    if (passphrase === undefined) {
      return JSON.stringify(
        { version: 1, encrypted: false, entries: this.entries() },
        null,
        2,
      );
    }

    const salt = nodeRandomBytes(16);
    const iv = nodeRandomBytes(12);
    const key = scryptSync(passphrase, salt, 32);
    const cipher = createCipheriv("aes-256-gcm", key, iv);
    cipher.setAAD(AUDIT_AAD);
    const plaintext = Buffer.from(JSON.stringify(this.entries()), "utf8");
    const encrypted = Buffer.concat([
      cipher.update(plaintext),
      cipher.final(),
    ]);
    const tag = cipher.getAuthTag();
    return JSON.stringify({
      version: 1,
      encrypted: true,
      algorithm: "aes-256-gcm",
      kdf: "scrypt",
      salt: salt.toString("base64"),
      iv: iv.toString("base64"),
      tag: tag.toString("base64"),
      data: encrypted.toString("base64"),
    });
  }

  static import(
    payload: string,
    passphrase?: string | Uint8Array,
  ): AuditVault {
    let parsed: unknown;
    try {
      parsed = JSON.parse(payload) as unknown;
    } catch {
      throw new AuditDecryptionError("Audit mapping is not valid JSON");
    }
    if (!parsed || typeof parsed !== "object") {
      throw new AuditDecryptionError("Audit mapping has an invalid shape");
    }
    if (Array.isArray(parsed)) {
      return AuditVault.fromEntries(parsed as AuditEntry[]);
    }

    const envelope = parsed as Record<string, unknown>;
    if (envelope.encrypted === true) {
      if (passphrase === undefined) {
        throw new AuditDecryptionError(
          "A passphrase is required for an encrypted audit mapping",
        );
      }
      const salt = Buffer.from(String(envelope.salt), "base64");
      const iv = Buffer.from(String(envelope.iv), "base64");
      const tag = Buffer.from(String(envelope.tag), "base64");
      const data = Buffer.from(String(envelope.data), "base64");
      try {
        const key = scryptSync(passphrase, salt, 32);
        const decipher = createDecipheriv("aes-256-gcm", key, iv);
        decipher.setAAD(AUDIT_AAD);
        decipher.setAuthTag(tag);
        const plaintext = Buffer.concat([
          decipher.update(data),
          decipher.final(),
        ]).toString("utf8");
        const entries = JSON.parse(plaintext) as AuditEntry[];
        return AuditVault.fromEntries(entries);
      } catch {
        throw new AuditDecryptionError();
      }
    }

    if (!Array.isArray(envelope.entries)) {
      throw new AuditDecryptionError("Audit mapping has no entries");
    }
    return AuditVault.fromEntries(envelope.entries as AuditEntry[]);
  }

  static fromEntries(entries: readonly AuditEntry[]): AuditVault {
    const vault = new AuditVault();
    for (const entry of entries) {
      if (!entry || typeof entry.token !== "string") {
        throw new AuditDecryptionError("Audit entry is missing a token");
      }
      vault.record({
        token: entry.token,
        path: entry.path,
        level: entry.level,
        reason: entry.reason,
        original: entry.original,
        sensitiveValue: entry.sensitiveValue,
        encoding: entry.encoding,
        strategy: entry.strategy,
        field: entry.field,
        createdAt: entry.createdAt,
      });
    }
    return vault;
  }

  private cloneEntry(entry: AuditEntry): AuditEntry {
    return {
      ...entry,
      original: deepClone(entry.original),
    };
  }
}

export function exportAuditMapping(
  vault: AuditVault,
  passphrase?: string | Uint8Array,
): string {
  return vault.export(passphrase);
}

export function importAuditMapping(
  payload: string,
  passphrase?: string | Uint8Array,
): AuditVault {
  return AuditVault.import(payload, passphrase);
}

export function hashValue(value: unknown): string {
  return createHash("sha256")
    .update(valueToText(value))
    .digest("hex");
}

export { TOKEN_PATTERN };
