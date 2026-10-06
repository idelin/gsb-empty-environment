import { CsvRecord, ParsedRow } from "./types";

function parseFields(text: string): string[][] {
  const rows: string[][] = [];
  let field = "";
  let row: string[] = [];
  let inQuotes = false;
  let i = 0;

  const pushField = () => {
    row.push(field);
    field = "";
  };
  const pushRow = () => {
    pushField();
    rows.push(row);
    row = [];
  };

  while (i < text.length) {
    const ch = text[i];
    if (inQuotes) {
      if (ch === '"') {
        if (text[i + 1] === '"') {
          field += '"';
          i += 2;
          continue;
        }
        inQuotes = false;
        i += 1;
        continue;
      }
      field += ch;
      i += 1;
      continue;
    }
    if (ch === '"') {
      inQuotes = true;
      i += 1;
      continue;
    }
    if (ch === ",") {
      pushField();
      i += 1;
      continue;
    }
    if (ch === "\r") {
      i += 1;
      continue;
    }
    if (ch === "\n") {
      pushRow();
      i += 1;
      continue;
    }
    field += ch;
    i += 1;
  }
  if (field.length > 0 || row.length > 0) {
    pushRow();
  }
  return rows;
}

export interface ParseResult {
  header: string[];
  rows: ParsedRow[];
}

/** Parses CSV text (header row required) into records keyed by column name. */
export function parseCsv(text: string): ParseResult {
  const table = parseFields(text);
  if (table.length === 0) {
    return { header: [], rows: [] };
  }
  const header = table[0];
  const rows: ParsedRow[] = [];
  for (let r = 1; r < table.length; r++) {
    const record: CsvRecord = {};
    const cells = table[r];
    for (let c = 0; c < header.length; c++) {
      record[header[c]] = c < cells.length ? cells[c] : "";
    }
    rows.push({ line: r + 1, record });
  }
  return { header, rows };
}
