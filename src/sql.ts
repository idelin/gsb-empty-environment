import type { RowImage } from "./types.ts";

/** Escape a JS value as a SQL literal. */
export function sqlLiteral(value: unknown): string {
  if (value === null || value === undefined) return "NULL";
  switch (typeof value) {
    case "number":
    case "bigint":
      return String(value);
    case "boolean":
      return value ? "TRUE" : "FALSE";
    case "string":
      return `'${value.replaceAll("'", "''")}'`;
    default:
      if (value instanceof Date) return `'${value.toISOString()}'`;
      return `'${JSON.stringify(value).replaceAll("'", "''")}'`;
  }
}

const ident = (name: string): string => `"${name.replaceAll('"', '""')}"`;

export function updateSql(
  table: string,
  pkColumn: string,
  pk: string | number,
  image: RowImage,
): string {
  const sets = Object.entries(image)
    .filter(([col]) => col !== pkColumn)
    .map(([col, v]) => `${ident(col)} = ${sqlLiteral(v)}`)
    .join(", ");
  return `UPDATE ${ident(table)} SET ${sets} WHERE ${ident(pkColumn)} = ${sqlLiteral(pk)};`;
}

export function deleteSql(
  table: string,
  pkColumn: string,
  pk: string | number,
): string {
  return `DELETE FROM ${ident(table)} WHERE ${ident(pkColumn)} = ${sqlLiteral(pk)};`;
}

export function insertSql(table: string, image: RowImage): string {
  const cols = Object.keys(image).map(ident).join(", ");
  const vals = Object.values(image).map(sqlLiteral).join(", ");
  return `INSERT INTO ${ident(table)} (${cols}) VALUES (${vals});`;
}
