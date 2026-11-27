import { ByteReader, PdfDocument, PdfFormatError } from './parser.ts';
import { findSignatures, validateByteRange } from './signatures.ts';
import { auditIncremental } from './audit.ts';
import type {
  AuditFinding,
  PdfVerificationReport,
  RevisionInfo,
  SignatureInfo,
} from './types.ts';

export { PdfDocument, ByteReader, PdfFormatError } from './parser.ts';
export { findSignatures, validateByteRange } from './signatures.ts';
export { auditIncremental, buildRoleGraph } from './audit.ts';
export type { ObjectRole } from './audit.ts';
export * from './types.ts';

function toRevisionInfo(doc: PdfDocument): RevisionInfo[] {
  return doc.revisions.map((revision) => ({
    index: revision.index,
    xrefOffset: revision.xrefOffset,
    revisionEnd: revision.revisionEnd,
    inUseObjectCount: [...revision.entries.values()].filter((entry) => entry.type !== 0).length,
    prevOffset: revision.prevOffset,
  }));
}

/**
 * Perform structural, anti-shadow verification of a signed PDF supplied as raw
 * bytes. Cryptographic PKCS#7 validation remains the caller's responsibility;
 * this routine guarantees that the signed byte ranges and the post-signature
 * incremental updates cannot hide a different document.
 */
export function verifyPdf(bytes: Uint8Array): PdfVerificationReport {
  const findings: AuditFinding[] = [];
  let doc: PdfDocument;
  try {
    doc = new PdfDocument(new ByteReader(bytes));
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    findings.push({ code: 'STRUCTURE_PARSE_ERROR', severity: 'error', message });
    return {
      valid: false,
      headerValid: false,
      fileSize: bytes.length,
      revisionCount: 0,
      revisions: [],
      signatures: [],
      findings,
      signedObjectCount: null,
    };
  }

  if (!doc.headerValid) {
    findings.push({
      code: 'HEADER_INVALID',
      severity: 'error',
      message: 'document does not begin with a %PDF- header',
    });
  }

  const lastEnd = doc.latestRevision.revisionEnd;
  if (lastEnd < bytes.length) {
    const tail = bytes.subarray(lastEnd);
    const hasContent = tail.some((b) => b !== 0x0a && b !== 0x0d && b !== 0x20 && b !== 0x09);
    if (hasContent) {
      findings.push({
        code: 'TAIL_GARBAGE',
        severity: 'error',
        message: `${bytes.length - lastEnd} non-whitespace bytes exist after the last %%EOF`,
      });
    }
  }

  const signatures: SignatureInfo[] = [];
  try {
    signatures.push(...findSignatures(doc));
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    findings.push({ code: 'STRUCTURE_PARSE_ERROR', severity: 'error', message });
  }
  if (signatures.length === 0) {
    findings.push({
      code: 'NO_SIGNATURE',
      severity: 'error',
      message: 'no /Sig or /DocTimeStamp dictionary found',
    });
  }

  let signedObjectCount: number | null = null;
  try {
    for (const signature of signatures) {
      validateByteRange(doc, signature, findings);
      if (signature.byteRangeValid) {
        const signedRevision = doc.revisions[signature.signingRevisionIndex];
        const count = [...signedRevision.entries.values()].filter(
          (entry) => entry.type !== 0,
        ).length;
        signedObjectCount = signedObjectCount === null ? count : Math.min(signedObjectCount, count);
        findings.push(...auditIncremental(doc, signature));
      }
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    findings.push({ code: 'STRUCTURE_PARSE_ERROR', severity: 'error', message });
  }

  const valid = doc.headerValid && signatures.length > 0 &&
    signatures.every((signature) => signature.byteRangeValid) &&
    findings.filter((finding) => finding.severity === 'error').length === 0;

  return {
    valid,
    headerValid: doc.headerValid,
    fileSize: bytes.length,
    revisionCount: doc.revisions.length,
    revisions: toRevisionInfo(doc),
    signatures,
    findings,
    signedObjectCount,
  };
}
