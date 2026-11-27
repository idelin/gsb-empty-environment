/**
 * Minimal, dependency-free hand-crafted PDF builder.
 *
 * It emits classic cross-reference tables and supports incremental updates,
 * giving the tests precise control over object numbers, byte offsets and the
 * exact /ByteRange geometry of signatures. The produced documents are not
 * cryptographically signed; placeholder PKCS#7 hex is sufficient because the
 * component under test verifies structure rather than cryptography.
 */

export interface BuiltObject {
  num: number;
  /** PDF object body WITHOUT the leading "N G obj" / trailing "endobj". */
  body: string;
}

export interface RevisionPlan {
  objects: BuiltObject[];
  /** Trailer entries rendered verbatim, e.g. `/Root 1 0 R`. */
  trailerLines: string[];
}

export interface IncrementalPlan extends RevisionPlan {
  /** Highest object number allocated so far (drives the subsection start). */
  previousMaxObject: number;
  /** Explicit trailer /Root reference, required on every revision. */
  rootRef: string;
}

const encoder = new TextEncoder();

function xrefEntry(offset: number, gen: number, inUse: boolean): string {
  const off = String(offset).padStart(10, '0');
  const g = String(gen).padStart(5, '0');
  return `${off} ${g} ${inUse ? 'n' : 'f'} \n`;
}

/** Render all planned revisions into one byte array using full xref tables. */
export function buildPdf(revisions: RevisionPlan[], header = '%PDF-1.7\n%\xff\xff\xff\xff\n'): Uint8Array {
  let output = encoder.encode(header);
  const allNumbers = new Set<number>();
  revisions.forEach((revision) => revision.objects.forEach((object) => allNumbers.add(object.num)));
  const size = Math.max(...allNumbers) + 1;

  let prevXref: number | null = null;
  for (let revIndex = 0; revIndex < revisions.length; revIndex += 1) {
    const plan = revisions[revIndex];
    const bodyOffsets = new Map<number, number>();
    for (const object of plan.objects) {
      bodyOffsets.set(object.num, output.length);
      output = concat(output, encoder.encode(`${object.num} 0 obj\n${object.body}\nendobj\n`));
    }
    const xrefOffset = output.length;
    const maxNum = Math.max(...plan.objects.map((object) => object.num));
    let table = 'xref\n';
    table += `0 ${maxNum + 1}\n`;
    table += xrefEntry(0, 65535, false);
    for (let num = 1; num <= maxNum; num += 1) {
      const offset = bodyOffsets.get(num);
      if (offset === undefined) {
        table += xrefEntry(0, 0, false);
      } else {
        table += xrefEntry(offset, 0, true);
      }
    }
    output = concat(output, encoder.encode(table));
    let trailer = 'trailer\n<<\n';
    trailer += `/Size ${size}\n`;
    if (prevXref !== null) trailer += `/Prev ${prevXref}\n`;
    for (const line of plan.trailerLines) trailer += `${line}\n`;
    trailer += '>>\nstartxref\n';
    trailer += `${xrefOffset}\n%%%%EOF\n`;
    output = concat(output, encoder.encode(trailer));
    prevXref = xrefOffset;
  }
  return output;
}

export function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  const merged = new Uint8Array(a.length + b.length);
  merged.set(a, 0);
  merged.set(b, a.length);
  return merged;
}

export function ascii(text: string): Uint8Array {
  return encoder.encode(text);
}

/**
 * Assemble a signature dictionary body with a fixed-width /ByteRange and a
 * zero-filled /Contents hole. `finalizeSignature` patches the real offsets
 * after the whole document has been laid out.
 */
export function signatureBody(contentsHexLength: number, extra = ''): string {
  const placeholder = '0'.repeat(contentsHexLength);
  return (
    '<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /adbe.pkcs7.detached ' +
    `/ByteRange [${'0'.repeat(10)} ${'0'.repeat(10)} ${'0'.repeat(10)} ${'0'.repeat(10)}] ` +
    `/Contents <${placeholder}>${extra} >>`
  );
}

export function timestampBody(contentsHexLength: number): string {
  const placeholder = '0'.repeat(contentsHexLength);
  return (
    '<< /Type /DocTimeStamp /Filter /Adobe.PPKLite /SubFilter /ETSI.RFC3161 ' +
    `/ByteRange [${'0'.repeat(10)} ${'0'.repeat(10)} ${'0'.repeat(10)} ${'0'.repeat(10)}] ` +
    `/Contents <${placeholder}>>`
  );
}

export interface SignatureGeometry {
  keywordPos: number;
  gapStart: number;
  gapEnd: number;
  contentsHexStart: number;
  contentsHexEnd: number;
}

/** Locate the geometry of a signature dictionary's /ByteRange + /Contents. */
export function locateSignature(bytes: Uint8Array, fromIndex = 0): SignatureGeometry {
  const keywordPos = indexOfBytes(bytes, ascii('/ByteRange'), fromIndex);
  if (keywordPos < 0) throw new Error('ByteRange not found');
  const open = indexOfBytes(bytes, ascii('['), keywordPos);
  const close = indexOfBytes(bytes, ascii(']'), open);
  const gapStart = keywordPos;
  const contentsOpen = indexOfBytes(bytes, ascii('<'), close);
  const contentsClose = indexOfBytes(bytes, ascii('>'), contentsOpen);
  return {
    keywordPos,
    gapStart,
    gapEnd: contentsClose + 1,
    contentsHexStart: contentsOpen + 1,
    contentsHexEnd: contentsClose,
  };
}

/**
 * Patch a laid-out document so its /ByteRange exactly covers all bytes except
 * the `/Contents <hex>` gap, then return the signed spans.
 */
export function finalizeSignature(
  bytes: Uint8Array,
  revisionEnd: number,
  fromIndex = 0,
): { bytes: Uint8Array; ranges: Array<[number, number]> } {
  const geometry = locateSignature(bytes, fromIndex);
  const gapStart = geometry.gapStart;
  const gapEnd = geometry.gapEnd;
  const off1 = 0;
  const len1 = gapStart;
  const off2 = gapEnd;
  const len2 = revisionEnd - gapEnd;
  const open = indexOfBytes(bytes, ascii('['), geometry.keywordPos);
  const close = indexOfBytes(bytes, ascii(']'), open);
  if (close - open !== 44) {
    throw new Error(`unexpected ByteRange width ${close - open}; expected 44`);
  }
  const slots: number[] = [off1, len1, off2, len2];
  const fields = slots.map((value) => {
    const valueText = String(value);
    if (valueText.length > 10) throw new Error('ByteRange slot too narrow');
    return valueText.padStart(10, '0');
  });
  const inner = ascii(`${fields[0]} ${fields[1]} ${fields[2]} ${fields[3]}`);
  const patched = bytes.slice();
  patched.set(inner, open + 1);
  return {
    bytes: patched,
    ranges: [
      [off1, off1 + len1],
      [off2, off2 + len2],
    ],
  };
}

export function indexOfBytes(haystack: Uint8Array, needle: Uint8Array, from = 0): number {
  outer: for (let i = from; i + needle.length <= haystack.length; i += 1) {
    for (let j = 0; j < needle.length; j += 1) {
      if (haystack[i + j] !== needle[j]) continue outer;
    }
    return i;
  }
  return -1;
}

export function lastIndexOfBytes(haystack: Uint8Array, needle: Uint8Array): number {
  for (let i = haystack.length - needle.length; i >= 0; i -= 1) {
    let matched = true;
    for (let j = 0; j < needle.length; j += 1) {
      if (haystack[i + j] !== needle[j]) {
        matched = false;
        break;
      }
    }
    if (matched) return i;
  }
  return -1;
}

/** Locate the byte offset immediately after the nth `%%EOF` (1-based). */
export function revisionEndAfter(bytes: Uint8Array, eofIndex: number): number {
  const marker = ascii('%%EOF');
  let pos = -marker.length;
  for (let i = 0; i < eofIndex; i += 1) {
    pos = indexOfBytes(bytes, marker, pos + marker.length);
    if (pos < 0) throw new Error('%%EOF not found');
  }
  let end = pos + marker.length;
  if (bytes[end] === 0x0d) end += 1;
  if (bytes[end] === 0x0a) end += 1;
  return end;
}

/**
 * Append one incremental update. Only the supplied (new or redefined) objects
 * appear in the new xref subsection; a second subsection frees nothing, and the
 * trailer carries a /Prev pointer to the previous revision's xref offset.
 */
export function appendIncremental(
  existing: Uint8Array,
  plan: IncrementalPlan,
  prevXrefOffset: number,
): { bytes: Uint8Array; xrefOffset: number } {
  let output = existing;
  const offsets = new Map<number, number>();
  for (const object of plan.objects) {
    offsets.set(object.num, output.length);
    output = concat(output, encoder.encode(`${object.num} 0 obj\n${object.body}\nendobj\n`));
  }
  const xrefOffset = output.length;
  const nums = plan.objects.map((object) => object.num).sort((a, b) => a - b);
  let table = 'xref\n';
  const groups: number[][] = [];
  let current: number[] = [];
  for (const num of nums) {
    if (current.length === 0 || num === current[current.length - 1] + 1) {
      current.push(num);
    } else {
      groups.push(current);
      current = [num];
    }
  }
  if (current.length > 0) groups.push(current);
  for (const group of groups) {
    table += `${group[0]} ${group.length}\n`;
    for (const num of group) {
      table += xrefEntry(offsets.get(num) as number, 0, true);
    }
  }
  const size = Math.max(plan.previousMaxObject, ...nums) + 1;
  table += `trailer\n<<\n/Size ${size}\n/Prev ${prevXrefOffset}\n`;
  table += `/Root ${plan.rootRef}\n`;
  for (const line of plan.trailerLines) table += `${line}\n`;
  table += '>>\nstartxref\n';
  table += `${xrefOffset}\n%%%%EOF\n`;
  return { bytes: concat(output, encoder.encode(table)), xrefOffset };
}

/** Find the byte offset of the last `startxref` target. */
export function lastStartXrefOffset(bytes: Uint8Array): number {
  const marker = ascii('startxref');
  const pos = lastIndexOfBytes(bytes, marker);
  if (pos < 0) throw new Error('startxref not found');
  let cursor = pos + marker.length;
  while (cursor < bytes.length && isWs(bytes[cursor])) cursor += 1;
  let digits = '';
  while (cursor < bytes.length && bytes[cursor] >= 0x30 && bytes[cursor] <= 0x39) {
    digits += String.fromCharCode(bytes[cursor]);
    cursor += 1;
  }
  if (digits === '') throw new Error('malformed startxref');
  return parseInt(digits, 10);
}

function isWs(b: number): boolean {
  return b === 0x00 || b === 0x09 || b === 0x0a || b === 0x0c || b === 0x0d || b === 0x20;
}
