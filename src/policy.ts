/**
 * MTA-STS policy fetch, parsing (RFC 8461 §3.2) and _mta-sts TXT record
 * parsing (RFC 8461 §3.1).
 */

import { HostnameError, parseNamePattern } from "./hostname.ts";

export type MtaStsMode = "enforce" | "testing" | "none";

export interface MtaStsPolicy {
  mode: MtaStsMode;
  /** Policy lifetime in seconds. */
  maxAge: number;
  /** MX hostnames or wildcard patterns permitted by the policy. */
  mx: string[];
}

export interface MtaStsTxtRecord {
  /** Policy id from the `id=` field of the _mta-sts TXT record. */
  id: string;
}

export class MtaStsPolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MtaStsPolicyError";
  }
}

const MODES: readonly MtaStsMode[] = ["enforce", "testing", "none"];

/**
 * Parse the body of an MTA-STS policy file. Throws MtaStsPolicyError on any
 * malformed directive, missing required field, invalid mode/max_age or
 * malformed MX pattern; a malformed policy must never overwrite the cache.
 */
export function parsePolicy(body: string): MtaStsPolicy {
  let mode: MtaStsMode | undefined;
  let maxAge: number | undefined;
  const mx: string[] = [];

  const lines = body.split(/\r?\n/);
  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (line === "") {
      continue;
    }
    const separator = line.indexOf(":");
    if (separator <= 0) {
      throw new MtaStsPolicyError(`malformed policy directive: ${line}`);
    }
    const key = line.slice(0, separator).trim().toLowerCase();
    const value = line.slice(separator + 1).trim();

    switch (key) {
      case "version":
        if (value !== "STSv1") {
          throw new MtaStsPolicyError(`unsupported policy version: ${value}`);
        }
        break;
      case "mode":
        if (!MODES.includes(value as MtaStsMode)) {
          throw new MtaStsPolicyError(`invalid policy mode: ${value}`);
        }
        mode = value as MtaStsMode;
        break;
      case "max_age": {
        if (!/^\d+$/.test(value)) {
          throw new MtaStsPolicyError(`invalid max_age: ${value}`);
        }
        maxAge = Number(value);
        if (!Number.isSafeInteger(maxAge) || maxAge < 0) {
          throw new MtaStsPolicyError(`invalid max_age: ${value}`);
        }
        break;
      }
      case "mx": {
        if (value.length === 0) {
          throw new MtaStsPolicyError("empty mx directive");
        }
        // Reject malformed wildcards (e.g. `*example.com`, `a.*.com`, bare `*`)
        // while parsing, so an attacker-supplied policy can never weaken checks.
        try {
          parseNamePattern(value);
        } catch (error) {
          if (error instanceof HostnameError) {
            throw new MtaStsPolicyError(error.message);
          }
          throw error;
        }
        if (!mx.includes(value.toLowerCase())) {
          mx.push(value.toLowerCase());
        }
        break;
      }
      default:
        // Unknown future directives with a key:value separator are ignored.
        break;
    }
  }

  if (mode === undefined) {
    throw new MtaStsPolicyError("policy is missing required mode directive");
  }
  if (maxAge === undefined) {
    throw new MtaStsPolicyError("policy is missing required max_age directive");
  }

  return { mode, maxAge, mx };
}

/**
 * Parse the TXT records published at `_mta-sts.<domain>`. Returns the first
 * well-formed STSv1 record, or null when none is present (record removal /
 * stripping). Malformed records are ignored, never treated as a valid id.
 */
export function parseStsTxtRecords(records: readonly string[]): MtaStsTxtRecord | null {
  for (const record of records) {
    const fields = record
      .split(";")
      .map((field) => field.trim())
      .filter((field) => field.length > 0);

    let version: string | undefined;
    let id: string | undefined;
    let valid = true;

    for (const field of fields) {
      const separator = field.indexOf("=");
      if (separator <= 0) {
        valid = false;
        break;
      }
      const key = field.slice(0, separator).trim().toLowerCase();
      const value = field.slice(separator + 1).trim();
      if (key === "v") {
        version = value;
      } else if (key === "id") {
        if (value.length === 0) {
          valid = false;
          break;
        }
        id = value;
      } else {
        valid = false;
        break;
      }
    }

    if (valid && version === "STSv1" && id !== undefined) {
      return { id };
    }
  }
  return null;
}
