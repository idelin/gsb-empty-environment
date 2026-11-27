import {
  ascii,
  appendIncremental,
  buildPdf,
  concat,
  finalizeSignature,
  indexOfBytes,
  lastStartXrefOffset,
  lastIndexOfBytes,
  revisionEndAfter,
  timestampBody,
  type BuiltObject,
} from './pdf-builder.ts';

const HEX_LEN = 64;

export interface BaselinePdf {
  bytes: Uint8Array;
  revisionEnd: number;
  objectNumbers: {
    root: number;
    pages: number;
    page: number;
    resources: number;
    font: number;
    content: number;
    acroForm: number;
    field: number;
    sig: number;
  };
}

/**
 * Build a single-revision, signed-looking contract. Object layout:
 * 1 catalog, 2 pages tree, 3 page, 4 resources, 5 font, 6 content stream,
 * 7 AcroForm, 8 merged signature field/widget, 9 signature dictionary.
 */
export function buildSignedContract(): BaselinePdf {
  const objectNumbers = {
    root: 1,
    pages: 2,
    page: 3,
    resources: 4,
    font: 5,
    content: 6,
    acroForm: 7,
    field: 8,
    sig: 9,
  };
  const contentStream = 'BT /F1 12 Tf 72 720 Td (Original contract terms) Tj ET';
  const objects = [
    {
      num: 1,
      body: '<< /Type /Catalog /Pages 2 0 R /AcroForm 7 0 R >>',
    },
    {
      num: 2,
      body: '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    },
    {
      num: 3,
      body: '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] ' +
        '/Resources 4 0 R /Contents 6 0 R /Annots [8 0 R] >>',
    },
    {
      num: 4,
      body: '<< /Font << /F1 5 0 R >> >>',
    },
    {
      num: 5,
      body: '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
    },
    {
      num: 6,
      body: `<< /Length ${contentStream.length} >>\nstream\n${contentStream}\nendstream`,
    },
    {
      num: 7,
      body: '<< /Fields [8 0 R] /SigFlags 3 >>',
    },
    {
      num: 8,
      body: '<< /FT /Sig /T (Signature1) /F 132 /Rect [0 0 0 0] ' +
        '/P 3 0 R /V 9 0 R >>',
    },
    {
      num: 9,
      body: sigDictionaryBody(),
    },
  ];
  const plan = {
    objects,
    trailerLines: ['/Root 1 0 R'],
  };
  const raw = buildPdf([plan]);
  const revisionEnd = revisionEndAfter(raw, 1);
  const finalized = finalizeSignature(raw, revisionEnd);
  return { bytes: finalized.bytes, revisionEnd, objectNumbers };
}

function sigDictionaryBody(): string {
  const placeholder = '0'.repeat(HEX_LEN);
  return '<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /adbe.pkcs7.detached ' +
    `/ByteRange [${'0'.repeat(10)} ${'0'.repeat(10)} ${'0'.repeat(10)} ${'0'.repeat(10)}] ` +
    `/Contents <${placeholder}>>`;
}

/** Append a legal document timestamp as a second revision. */
export function buildWithDocTimestamp(baseline: BaselinePdf): Uint8Array {
  const timestampField = 10;
  const timestampSig = 11;
  const updatedAcroForm = {
    num: 7,
    body: '<< /Fields [8 0 R 10 0 R] /SigFlags 3 >>',
  };
  const pageWithAnnot = {
    num: 3,
    body: '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] ' +
      '/Resources 4 0 R /Contents 6 0 R /Annots [8 0 R 10 0 R] >>',
  };
  const objects: BuiltObject[] = [
    updatedAcroForm,
    pageWithAnnot,
    {
      num: timestampField,
      body: '<< /FT /Sig /T (Timestamp1) /F 132 /Rect [0 0 0 0] /P 3 0 R /V 11 0 R >>',
    },
    { num: timestampSig, body: timestampBody(HEX_LEN) },
  ];
  const appended = appendOne(baseline, objects);
  const secondEnd = revisionEndAfter(appended, 2);
  return finalizeSignature(appended, secondEnd, baseline.bytes.length).bytes;
}

/** Append a legal /DSS revocation-information revision. */
export function buildWithDss(baseline: BaselinePdf): Uint8Array {
  const dssObject = 10;
  const certObject = 11;
  const certStream = '0'.repeat(32);
  const updatedRoot = {
    num: 1,
    body: '<< /Type /Catalog /Pages 2 0 R /AcroForm 7 0 R /DSS 10 0 R >>',
  };
  const objects: BuiltObject[] = [
    updatedRoot,
    { num: dssObject, body: '<< /Type /DSS /Certs [11 0 R] >>' },
    {
      num: certObject,
      body: `<< /Length ${certStream.length} >>\nstream\n${certStream}\nendstream`,
    },
  ];
  return appendOne(baseline, objects);
}

/** Hide attack: redefine the page tree so the signed page is removed from /Kids. */
export function buildHideAttack(baseline: BaselinePdf): Uint8Array {
  const decoyPage = 10;
  const decoyContent = 11;
  const objects: BuiltObject[] = [
    {
      num: 2,
      body: '<< /Type /Pages /Kids [10 0 R] /Count 1 >>',
    },
    {
      num: decoyPage,
      body: '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] ' +
        '/Resources 4 0 R /Contents 11 0 R >>',
    },
    {
      num: decoyContent,
      body: '<< /Length 40 >>\nstream\nBT (Hidden replacement page) Tj ET\nendstream',
    },
  ];
  return appendOne(baseline, objects);
}

/** Replace attack: keep the same page object number but swap its /Contents. */
export function buildReplaceAttack(baseline: BaselinePdf): Uint8Array {
  const evilContent = 10;
  const evilText = 'BT (Tampered contract - pay 10x) Tj ET';
  const objects: BuiltObject[] = [
    {
      num: 3,
      body: '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] ' +
        '/Resources 4 0 R /Contents 10 0 R /Annots [8 0 R] >>',
    },
    {
      num: evilContent,
      body: `<< /Length ${evilText.length} >>\nstream\n${evilText}\nendstream`,
    },
  ];
  return appendOne(baseline, objects);
}

/** Hide-and-Replace: new catalog points at a replacement page tree. */
export function buildHideAndReplaceAttack(baseline: BaselinePdf): Uint8Array {
  const evilRoot = 10;
  const evilPages = 11;
  const evilPage = 12;
  const evilContent = 13;
  const text = 'BT (Completely different document) Tj ET';
  const objects: BuiltObject[] = [
    {
      num: evilRoot,
      body: '<< /Type /Catalog /Pages 11 0 R /AcroForm 7 0 R >>',
    },
    { num: evilPages, body: '<< /Type /Pages /Kids [12 0 R] /Count 1 >>' },
    {
      num: evilPage,
      body: '<< /Type /Page /Parent 11 0 R /MediaBox [0 0 612 792] ' +
        '/Resources 4 0 R /Contents 13 0 R >>',
    },
    { num: evilContent, body: `<< /Length ${text.length} >>\nstream\n${text}\nendstream` },
  ];
  return appendOne(baseline, objects, '10 0 R');
}

/** Annotation tamper: replace the signed widget annotation in place. */
export function buildAnnotationTamper(baseline: BaselinePdf): Uint8Array {
  const objects: BuiltObject[] = [
    {
      num: 8,
      body: '<< /FT /Sig /T (Signature1) /F 132 /Rect [10 10 200 80] ' +
        '/P 3 0 R /V 9 0 R >>',
    },
  ];
  return appendOne(baseline, objects);
}

/** Font / resource replacement attack. */
export function buildFontReplaceAttack(baseline: BaselinePdf): Uint8Array {
  const evilFont = 10;
  const objects: BuiltObject[] = [
    { num: 4, body: '<< /Font << /F1 10 0 R >> >>' },
    { num: evilFont, body: '<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>' },
  ];
  return appendOne(baseline, objects);
}

/** Inject an orphan object that is not reachable from any allowed new subtree. */
export function buildOrphanObjectAttack(baseline: BaselinePdf): Uint8Array {
  const objects: BuiltObject[] = [
    { num: 10, body: '<< /Type /Page /MediaBox [0 0 1 1] >>' },
  ];
  return appendOne(baseline, objects);
}

/** Append extra, non-whitespace garbage immediately after the signing EOF. */
export function buildTailGarbage(baseline: BaselinePdf): Uint8Array {
  return concat(baseline.bytes, ascii('\n%GARBAGE-PAYLOAD\n'));
}

/**
 * Tamper with the /ByteRange so a non-zero leading offset leaves the document
 * header outside the signed region (classic "hole" attack).
 */
export function buildByteRangeNotFromZero(baseline: BaselinePdf): Uint8Array {
  const bytes = baseline.bytes.slice();
  const open = indexOfBytes(bytes, ascii('/ByteRange ['));
  if (open < 0) throw new Error('ByteRange missing');
  const bracket = open + '/ByteRange '.length;
  bytes[bracket + 1] = 0x31;
  return bytes;
}

/**
 * Shrink the second /ByteRange length so trailing signed bytes (the xref +
 * trailer) are no longer covered, leaving room for undetected tampering.
 */
export function buildByteRangeTruncated(baseline: BaselinePdf): Uint8Array {
  const bytes = baseline.bytes.slice();
  const open = indexOfBytes(bytes, ascii('/ByteRange ['));
  const bracket = open + '/ByteRange '.length;
  const close = indexOfBytes(bytes, ascii(']'), bracket);
  const fourthSlot = close - 10;
  bytes[fourthSlot + 9] = 0x30;
  bytes[fourthSlot + 8] = 0x30;
  return bytes;
}

/**
 * Smuggle an extra dictionary entry / hidden reference inside the unsigned
 * /Contents gap while keeping every xref offset stable. The hex string is
 * shortened by exactly the payload width, so the document still parses and the
 * gap whitelist (not a generic parse failure) must detect the tampering.
 */
export function buildByteRangeHiddenObjectInGap(baseline: BaselinePdf): Uint8Array {
  const bytes = baseline.bytes.slice();
  const byteRangePos = indexOfBytes(bytes, ascii('/ByteRange'));
  const contentsPos = indexOfBytes(bytes, ascii('/Contents'), byteRangePos);
  const openAngle = indexOfBytes(bytes, ascii('<'), contentsPos);
  const closeAngle = indexOfBytes(bytes, ascii('>'), openAngle);
  const dictClose = closeAngle + 1;
  const originalSpan = dictClose - openAngle;
  const payload = ascii('/Shadow 99 0 R');
  const newHexLength = originalSpan - 3 - payload.length;
  const newHex = ascii('0'.repeat(Math.max(0, newHexLength)));
  const rebuilt = new Uint8Array(originalSpan);
  rebuilt[0] = 0x3c;
  rebuilt.set(newHex, 1);
  rebuilt[1 + newHex.length] = 0x3e;
  rebuilt.set(payload, 2 + newHex.length);
  rebuilt[originalSpan - 1] = 0x3e;
  if (2 + newHex.length + payload.length + 1 !== originalSpan) {
    throw new Error('gap smuggle width mismatch');
  }
  bytes.set(rebuilt, openAngle);
  return bytes;
}

/** Embed an explicit indirect-object token inside the unsigned gap. */
export function buildByteRangeObjectTokenInGap(baseline: BaselinePdf): Uint8Array {
  const bytes = baseline.bytes.slice();
  const byteRangePos = indexOfBytes(bytes, ascii('/ByteRange'));
  const contentsPos = indexOfBytes(bytes, ascii('/Contents'), byteRangePos);
  const openAngle = indexOfBytes(bytes, ascii('<'), contentsPos);
  const closeAngle = indexOfBytes(bytes, ascii('>'), openAngle);
  const dictClose = closeAngle + 1;
  const originalSpan = dictClose - openAngle;
  const payload = ascii('99 0 obj endobj');
  const newHexLength = originalSpan - 3 - payload.length;
  const newHex = ascii('0'.repeat(Math.max(0, newHexLength)));
  const rebuilt = new Uint8Array(originalSpan);
  rebuilt[0] = 0x3c;
  rebuilt.set(newHex, 1);
  rebuilt[1 + newHex.length] = 0x3e;
  rebuilt.set(payload, 2 + newHex.length);
  rebuilt[originalSpan - 1] = 0x3e;
  bytes.set(rebuilt, openAngle);
  return bytes;
}

function appendOne(
  baseline: BaselinePdf,
  objects: BuiltObject[],
  rootRef = '1 0 R',
): Uint8Array {
  const prevXref = lastStartXrefOffset(baseline.bytes);
  const result = appendIncremental(
    baseline.bytes,
    {
      objects,
      trailerLines: [],
      previousMaxObject: highestBaselineObject(baseline),
      rootRef,
    },
    prevXref,
  );
  return result.bytes;
}

function highestBaselineObject(baseline: BaselinePdf): number {
  return Math.max(...Object.values(baseline.objectNumbers));
}
