import { QueryStructureError } from './errors.ts';
import { IDENTIFIER_RE } from './ident.ts';

export interface TableEntry {
  schema?: string;
  name: string;
  columns: readonly string[];
}

export interface IdentifierWhitelist {
  tables: readonly TableEntry[];
}

export interface ResolvedTable {
  key: string;
  schema?: string;
  name: string;
  columns: ReadonlySet<string>;
}

export function compileWhitelist(whitelist: IdentifierWhitelist): Map<string, ResolvedTable> {
  const tables = new Map<string, ResolvedTable>();
  for (const entry of whitelist.tables) {
    if (
      entry === null ||
      typeof entry !== 'object' ||
      typeof entry.name !== 'string' ||
      !IDENTIFIER_RE.test(entry.name)
    ) {
      throw new QueryStructureError('whitelist table entry has an invalid name');
    }
    if (entry.schema !== undefined && !IDENTIFIER_RE.test(entry.schema)) {
      throw new QueryStructureError(`whitelist table ${entry.name} has an invalid schema`);
    }
    if (!Array.isArray(entry.columns) || entry.columns.length === 0) {
      throw new QueryStructureError(`whitelist table ${entry.name} must declare columns`);
    }
    const columns = new Set<string>();
    for (const column of entry.columns) {
      if (!IDENTIFIER_RE.test(column)) {
        throw new QueryStructureError(`whitelist table ${entry.name} has an invalid column`);
      }
      columns.add(column);
    }
    const key = entry.schema === undefined ? entry.name : `${entry.schema}.${entry.name}`;
    if (tables.has(key)) {
      throw new QueryStructureError(`duplicate whitelist table: ${key}`);
    }
    tables.set(key, {
      key,
      schema: entry.schema,
      name: entry.name,
      columns,
    });
  }
  if (tables.size === 0) {
    throw new QueryStructureError('identifier whitelist must contain at least one table');
  }
  return tables;
}
