import type {
  AuditFinding,
  PdfDict,
  PdfObject,
  PdfRevision,
  SignatureInfo,
} from './types.ts';
import type { PdfDocument } from './parser.ts';

const isWhitespaceByte = (b: number): boolean =>
  b === 0x00 || b === 0x09 || b === 0x0a || b === 0x0c || b === 0x0d || b === 0x20;

const isHexByte = (b: number): boolean =>
  (b >= 0x30 && b <= 0x39) || (b >= 0x41 && b <= 0x46) || (b >= 0x61 && b <= 0x66);

function asDict(obj: PdfObject | null): PdfDict | null {
  if (obj && obj.kind === 'dict') return obj;
  if (obj && obj.kind === 'stream') return obj.dict;
  return null;
}

function nameValue(obj: PdfObject | undefined): string | null {
  return obj && obj.kind === 'name' ? obj.value : null;
}

const nameOf = nameValue;

/** Locate every signature / document-timestamp dictionary across all revisions. */
export function findSignatures(doc: PdfDocument): SignatureInfo[] {
  const candidates = new Map<number, { field: PdfDict; sig: PdfDict; sigNum: number }>();
  const widgetFieldNumbers = new Set<number>();
  for (let rev = 0; rev < doc.revisions.length; rev += 1) {
    const revision = doc.revisions[rev];
    for (const num of revision.entries.keys()) {
      let obj: PdfObject | null;
      try {
        obj = doc.getObject(num, rev);
      } catch {
        continue;
      }
      const dict = asDict(obj);
      if (!dict) continue;
      const fieldType = nameValue(dict.entries.get('FT'));
      const sigType = nameValue(dict.entries.get('Type'));
      const isSigField = fieldType === 'Sig' || sigType === 'Sig' || sigType === 'DocTimeStamp';
      if (!isSigField) continue;
      const vRef = dict.entries.get('V');
      if (vRef && vRef.kind === 'ref') {
        widgetFieldNumbers.add(num);
        let resolved: PdfObject | null = null;
        try {
          resolved = doc.getObject(vRef.num, rev);
        } catch {
          continue;
        }
        const sigDict = asDict(resolved);
        if (sigDict && !candidates.has(vRef.num)) {
          candidates.set(vRef.num, { field: dict, sig: sigDict, sigNum: vRef.num });
        }
      } else if (dict.entries.has('ByteRange') || dict.entries.has('Contents')) {
        if (!candidates.has(num)) {
          candidates.set(num, { field: dict, sig: dict, sigNum: num });
        }
      }
    }
  }
  return [...candidates.values()]
    .filter((candidate) => !widgetFieldNumbers.has(candidate.sigNum))
    .map((candidate) => {
      const merged = mergeFieldAndSig(candidate.field, candidate.sig);
      return buildSignatureInfo(
        candidate.sigNum,
        merged,
        nameOf(merged.entries.get('Type')) === 'DocTimeStamp' ? 'doc-timestamp' : 'signature',
      );
    })
    .sort((a, b) => a.objectNumber - b.objectNumber);
}

/** Merge widget/field entries with the referenced signature dictionary. */
function mergeFieldAndSig(field: PdfDict, sig: PdfDict): PdfDict {
  if (field === sig) return field;
  const entries = new Map<string, PdfObject>(field.entries);
  for (const [key, value] of sig.entries) entries.set(key, value);
  return { kind: 'dict', entries };
}

function buildSignatureInfo(
  objectNumber: number,
  dict: PdfDict,
  type: SignatureInfo['type'],
): SignatureInfo {
  const fieldNameObj = dict.entries.get('T');
  const byteRangeObj = dict.entries.get('ByteRange');
  const contentsObj = dict.entries.get('Contents');
  const byteRange: number[] = [];
  if (byteRangeObj && byteRangeObj.kind === 'array') {
    for (const item of byteRangeObj.items) {
      if (item.kind !== 'num') {
        byteRange.length = 0;
        break;
      }
      byteRange.push(item.value);
    }
  }
  const contentsHex =
    contentsObj && contentsObj.kind === 'string'
      ? new TextDecoder('latin1').decode(contentsObj.value)
      : '';
  return {
    objectNumber,
    fieldName: fieldNameObj && fieldNameObj.kind === 'string'
      ? new TextDecoder('latin1').decode(fieldNameObj.value)
      : null,
    type,
    filter: nameValue(dict.entries.get('Filter')),
    subFilter: nameValue(dict.entries.get('SubFilter')),
    byteRange,
    contentsHex,
    signedDataRanges: [],
    signingRevisionIndex: -1,
    byteRangeValid: false,
    cryptographic: 'not-verified',
  };
}

/**
 * Strictly validate the /ByteRange coverage of one signature.
 *
 * Rules enforced:
 *  - exactly four non-negative integers `[off1 len1 off2 len2]`;
 *  - `off1 === 0` and `off1 + len1 < off2`;
 *  - the only gap is the `/Contents <hex...>` token, with no smuggled objects;
 *  - the covered suffix runs to the end of the signing revision's `%%EOF`,
 *    leaving no uncovered tail garbage.
 */
export function validateByteRange(
  doc: PdfDocument,
  info: SignatureInfo,
  findings: AuditFinding[],
): void {
  const data = doc.data;
  const range = info.byteRange;
  const push = (code: string, message: string): void => {
    findings.push({ code, severity: 'error', message, objectNumber: info.objectNumber });
  };

  if (range.length !== 4) {
    push('BYTERANGE_MALFORMED', '/ByteRange must contain exactly four integers');
    return;
  }
  const [off1, len1, off2, len2] = range;
  if (range.some((value) => !Number.isInteger(value) || value < 0)) {
    push('BYTERANGE_MALFORMED', '/ByteRange entries must be non-negative integers');
    return;
  }
  const geometryValid = off1 === 0 && off1 + len1 < off2 && off2 + len2 <= data.length;
  if (off1 !== 0) {
    push('BYTERANGE_NOT_FROM_ZERO', '/ByteRange must start at byte offset 0');
  }
  if (off1 + len1 >= off2) {
    push('BYTERANGE_EMPTY_GAP', '/ByteRange gap (the /Contents hole) must be non-empty');
  }
  if (off2 + len2 > data.length) {
    push('BYTERANGE_OUT_OF_BOUNDS', '/ByteRange extends past the end of the document');
  }

  if (geometryValid) {
    const gapStart = off1 + len1;
    const gapEnd = off2;
    const gapError = inspectContentsGap(data, gapStart, gapEnd);
    if (gapError) {
      push('BYTERANGE_GAP_INVALID', gapError);
    }
    const gap = data.subarray(gapStart, gapEnd);
    if (containsSmuggledStructure(gap)) {
      push(
        'BYTERANGE_HIDDEN_OBJECT',
        'hidden object or xref structure smuggled inside /Contents gap',
      );
    }
  }

  const signingRevision = identifySigningRevision(doc, off2 + len2);
  if (!signingRevision) {
    push('BYTERANGE_NO_SNAPSHOT', 'signed bytes do not match any document revision');
    info.byteRangeValid = !hasErrorFor(info, findings);
    return;
  }
  info.signingRevisionIndex = signingRevision.index;
  info.signedDataRanges = [
    [off1, off1 + len1],
    [off2, off2 + len2],
  ];

  const endDelta = signingRevision.revisionEnd - (off2 + len2);
  if (endDelta < 0) {
    push('BYTERANGE_TRUNCATED', 'signature does not cover the full signing revision');
  } else if (endDelta > 2) {
    push(
      'BYTERANGE_TAIL_GARBAGE',
      `uncovered ${endDelta} bytes between signed range and revision %%EOF`,
    );
  }

  info.byteRangeValid = !hasErrorFor(info, findings);
}

function hasErrorFor(info: SignatureInfo, findings: AuditFinding[]): boolean {
  return findings.some(
    (finding) => finding.objectNumber === info.objectNumber && finding.severity === 'error',
  );
}

/**
 * Validate the unsigned gap. In a conforming PDF it contains exactly the
 * `/ByteRange [...]` entry followed by `/Contents <hex>`, surrounded only by
 * whitespace. Nothing else (no smuggled `obj`, nested dict or page tokens) may
 * appear between the two signed spans.
 */
function inspectContentsGap(
  data: Uint8Array,
  gapStart: number,
  gapEnd: number,
): string | null {
  let pos = gapStart;
  const skipWs = (): void => {
    while (pos < gapEnd && isWhitespaceByte(data[pos])) pos += 1;
  };
  skipWs();

  if (data[pos] === 0x2f) {
    const byteRange = '/ByteRange';
    if (pos + byteRange.length <= gapEnd &&
      data.subarray(pos, pos + byteRange.length).every((b, i) => b === byteRange.charCodeAt(i))) {
      pos += byteRange.length;
      skipWs();
      if (data[pos] !== 0x5b) return '/ByteRange in gap must be an array';
      let depth = 0;
      do {
        if (data[pos] === 0x5b) depth += 1;
        if (data[pos] === 0x5d) depth -= 1;
        pos += 1;
      } while (pos < gapEnd && depth > 0);
      skipWs();
    }
  }

  const keyword = '/Contents';
  if (pos + keyword.length > gapEnd) {
    return 'gap does not contain /Contents';
  }
  for (let i = 0; i < keyword.length; i += 1) {
    if (data[pos + i] !== keyword.charCodeAt(i)) {
      return 'gap may contain only /ByteRange and /Contents entries';
    }
  }
  pos += keyword.length;
  skipWs();
  if (pos >= gapEnd || data[pos] !== 0x3c) {
    return '/Contents value must be a hexadecimal string';
  }
  pos += 1;
  const hexStart = pos;
  while (pos < gapEnd && isHexByte(data[pos])) pos += 1;
  const hexEnd = pos;
  if (hexEnd === hexStart) return '/Contents hex string is empty';
  if ((hexEnd - hexStart) % 2 !== 0) return '/Contents hex string has odd length';
  if (pos >= gapEnd || data[pos] !== 0x3e) {
    return 'non-hex data or missing closing ">" in /Contents gap';
  }
  pos += 1;
  skipWs();
  if (pos !== gapEnd) {
    return 'unexpected trailing bytes inside /Contents gap';
  }
  return null;
}

function containsSmuggledStructure(gap: Uint8Array): boolean {
  const text = new TextDecoder('latin1').decode(gap);
  return /\bobj\b|\bendobj\b|\bxref\b|startxref|<<|>>|\/Type\s*\/Page\b/.test(text);
}

function identifySigningRevision(doc: PdfDocument, signedEnd: number): PdfRevision | null {
  let best: PdfRevision | null = null;
  for (const revision of doc.revisions) {
    const delta = revision.revisionEnd - signedEnd;
    if (delta < -2 || delta > 2) continue;
    if (!best || Math.abs(delta) < Math.abs(best.revisionEnd - signedEnd)) {
      best = revision;
    }
  }
  return best;
}
