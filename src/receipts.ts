import { createHmac } from "node:crypto";
import {
  canonicalJson,
  safeEqualString,
  SCHEMA,
  sha256Hex,
  type Finding,
} from "./core.ts";
import type {
  AuthenticatedReceipt,
  PrefixReceipt,
  ReceiptAuthenticator,
} from "./types.ts";

export class HmacReceiptAuthenticator implements ReceiptAuthenticator {
  readonly alg = "HS256-SHA256/v1";
  private readonly secret: string | Uint8Array;
  readonly keyId: string;

  constructor(secret: string | Uint8Array, keyId = "host-receipt-key-v1") {
    this.secret = secret;
    this.keyId = keyId;
  }

  authenticate(receipt: PrefixReceipt): AuthenticatedReceipt {
    const mac = createHmac("sha256", this.secret).update(canonicalJson(receipt)).digest("hex");
    return {
      ...receipt,
      auth: { alg: this.alg, keyId: this.keyId, mac },
    };
  }

  verify(receipt: AuthenticatedReceipt): boolean {
    if (receipt.auth?.alg !== this.alg || receipt.auth.keyId !== this.keyId) return false;
    const { auth, ...payload } = receipt;
    const expected = createHmac("sha256", this.secret)
      .update(canonicalJson(payload satisfies PrefixReceipt))
      .digest("hex");
    return safeEqualString(expected, receipt.auth.mac);
  }
}

export interface AuditReceiptOptions {
  prefix?: string | Uint8Array;
  authenticator?: ReceiptAuthenticator;
  requireAuthenticated?: boolean;
  expectedOldTemplateHash?: string;
  expectedStackSummaryHash?: string;
}

export function prefixByteLength(prefix: string | Uint8Array): number {
  return typeof prefix === "string" ? Buffer.byteLength(prefix, "utf8") : prefix.byteLength;
}

export function prefixSha256(prefix: string | Uint8Array): string {
  const bytes = typeof prefix === "string" ? Buffer.from(prefix, "utf8") : prefix;
  return sha256Hex(bytes);
}

export function auditReceipt(
  receipt: AuthenticatedReceipt | PrefixReceipt,
  options: AuditReceiptOptions = {},
): Finding[] {
  const findings: Finding[] = [];

  if (receipt.schema !== SCHEMA.receipt && receipt.schema !== SCHEMA.authenticatedReceipt) {
    findings.push({
      code: "RECEIPT_SCHEMA_INVALID",
      severity: "error",
      message: "Receipt has an unsupported schema",
    });
  }

  const authenticated = "auth" in receipt;
  if (options.requireAuthenticated !== false) {
    if (!authenticated) {
      findings.push({
        code: "RECEIPT_UNAUTHENTICATED",
        severity: "warning",
        message: "Published prefix receipt is not authenticated",
      });
    } else if (!options.authenticator) {
      findings.push({
        code: "RECEIPT_VERIFIER_KEY_MISSING",
        severity: "warning",
        message: "No authenticator is available to verify the receipt",
      });
    } else if (!options.authenticator.verify(receipt as AuthenticatedReceipt)) {
      findings.push({
        code: "RECEIPT_MAC_INVALID",
        severity: "error",
        message: "Receipt authentication failed",
      });
    }
  }

  if (
    options.expectedOldTemplateHash &&
    receipt.oldTemplateHash !== options.expectedOldTemplateHash
  ) {
    findings.push({
      code: "RECEIPT_OLD_TEMPLATE_MISMATCH",
      severity: "error",
      message: "Receipt was issued for a different frozen old template",
    });
  }

  if (options.expectedStackSummaryHash) {
    if (!receipt.stackSummarySha256) {
      findings.push({
        code: "RECEIPT_STACK_BINDING_MISSING",
        severity: "warning",
        message: "Receipt does not bind the stack summary digest",
      });
    } else if (receipt.stackSummarySha256 !== options.expectedStackSummaryHash) {
      findings.push({
        code: "RECEIPT_STACK_BINDING_MISMATCH",
        severity: "error",
        message: "Stack summary digest does not match authenticated receipt binding",
      });
    }
  }

  if (!Number.isInteger(receipt.byteLength) || receipt.byteLength < 0) {
    findings.push({
      code: "RECEIPT_LENGTH_INVALID",
      severity: "error",
      message: "Receipt byte length is invalid",
    });
  }

  if (options.prefix !== undefined) {
    const actualLength = prefixByteLength(options.prefix);
    if (actualLength !== receipt.byteLength) {
      findings.push({
        code: "RECEIPT_PREFIX_LENGTH_MISMATCH",
        severity: "error",
        message: "Supplied prefix length does not match receipt",
        evidence: { actualLength, receiptLength: receipt.byteLength },
      });
    }
    const actualHash = prefixSha256(options.prefix);
    if (actualHash !== receipt.prefixSha256) {
      findings.push({
        code: "RECEIPT_PREFIX_HASH_MISMATCH",
        severity: "error",
        message: "Supplied prefix does not match authenticated prefix hash",
      });
    }
  }

  const sorted = [...receipt.segments].sort((a, b) => a.ordinal - b.ordinal);
  let cursor = 0;
  sorted.forEach((segment, index) => {
    if (segment.ordinal !== index) {
      findings.push({
        code: "RECEIPT_SEGMENT_ORDER_INVALID",
        severity: "error",
        message: `Segment ordinal ${segment.ordinal} is out of order`,
      });
    }
    if (segment.start !== cursor) {
      findings.push({
        code: "RECEIPT_SEGMENT_GAP",
        severity: "error",
        message: `Segment ${segment.nodeId} does not begin at ${cursor}`,
        evidence: { start: segment.start, expected: cursor },
      });
    }
    if (segment.end < segment.start) {
      findings.push({
        code: "RECEIPT_SEGMENT_RANGE_INVALID",
        severity: "error",
        message: `Segment ${segment.nodeId} has negative length`,
      });
    }
    if (segment.end > receipt.byteLength) {
      findings.push({
        code: "RECEIPT_SEGMENT_OUT_OF_BOUNDS",
        severity: "error",
        message: `Segment ${segment.nodeId} exceeds receipt length`,
      });
    }
    if (!segment.contextId || !segment.sourceLabel && segment.kind === "field") {
      findings.push({
        code: "RECEIPT_SEGMENT_ORIGIN_INCOMPLETE",
        severity: "warning",
        message: `Field segment ${segment.nodeId} is missing source or context`,
      });
    }

    if (options.prefix !== undefined && typeof options.prefix === "string") {
      const bytes = Buffer.from(options.prefix, "utf8");
      const slice = bytes.subarray(segment.start, segment.end);
      if (sha256Hex(slice) !== segment.emittedSha256) {
        findings.push({
          code: "RECEIPT_SEGMENT_HASH_MISMATCH",
          severity: "error",
          message: `Segment ${segment.nodeId} hash does not match prefix bytes`,
        });
      }
    }
    cursor = Math.max(cursor, segment.end);
  });

  if (cursor !== receipt.byteLength) {
    findings.push({
      code: "RECEIPT_COVERAGE_INCOMPLETE",
      severity: "error",
      message: "Receipt segments do not cover the complete published prefix",
      evidence: { coveredBytes: cursor, byteLength: receipt.byteLength },
    });
  }

  const last = sorted[sorted.length - 1];
  if (last && last.contextId !== receipt.finalContextId) {
    findings.push({
      code: "RECEIPT_FINAL_CONTEXT_MISMATCH",
      severity: "error",
      message: "Receipt final context does not match last segment context",
    });
  }

  return findings;
}
