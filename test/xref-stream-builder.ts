const encoder = new TextEncoder();

/**
 * Build a small, unsigned PDF 1.5 document that uses a cross-reference stream
 * instead of a classic xref table, exercising the xref-stream parser path.
 */
export function buildXrefStreamPdf(): Uint8Array {
  const parts: Uint8Array[] = [];
  const push = (text: string): void => {
    parts.push(encoder.encode(text));
  };
  const currentLength = (): number => parts.reduce((sum, part) => sum + part.length, 0);

  push('%PDF-1.5\n');
  const obj1Offset = currentLength();
  push('1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n');
  const obj2Offset = currentLength();
  push('2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n');
  const xrefOffset = currentLength();

  const typeWidth = 1;
  const offsetWidth = 4;
  const genWidth = 2;
  const rows: number[] = [];
  const writeRow = (type: number, field2: number, field3: number): void => {
    rows.push(type);
    for (let i = offsetWidth - 1; i >= 0; i -= 1) rows.push((field2 >> (8 * i)) & 0xff);
    for (let i = genWidth - 1; i >= 0; i -= 1) rows.push((field3 >> (8 * i)) & 0xff);
  };
  writeRow(0, 0, 0);
  writeRow(1, obj1Offset, 0);
  writeRow(1, obj2Offset, 0);
  writeRow(1, xrefOffset, 0);
  const streamBytes = Uint8Array.from(rows);

  push(
    '3 0 obj\n' +
      `<< /Type /XRef /Size 4 /W [${typeWidth} ${offsetWidth} ${genWidth}] ` +
      `/Root 1 0 R /Length ${streamBytes.length} >>\n`,
  );
  push('stream\n');
  parts.push(streamBytes);
  push('\nendstream\nendobj\n');
  push(`startxref\n${xrefOffset}\n%%EOF\n`);

  const output = new Uint8Array(currentLength());
  let cursor = 0;
  for (const part of parts) {
    output.set(part, cursor);
    cursor += part.length;
  }
  return output;
}
