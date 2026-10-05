import { Counterexample, SearchResult } from './search';
import { Database, Row, Value } from './types';

function fmtValue(v: Value): string {
  return typeof v === 'string' ? JSON.stringify(v) : String(v);
}

export function fmtRow(row: Row, attrs?: string[]): string {
  const keys = attrs ?? Object.keys(row);
  return '(' + keys.map((k) => `${k}=${fmtValue(row[k])}`).join(', ') + ')';
}

function fmtTable(rows: Row[], indent: string): string {
  if (rows.length === 0) return indent + '(empty)';
  return rows.map((r) => indent + fmtRow(r)).join('\n');
}

export function fmtDatabase(db: Database): string {
  const lines: string[] = [];
  for (const name of Object.keys(db)) {
    lines.push(`${name} (${db[name].length} rows):`);
    lines.push(fmtTable(db[name], '  '));
  }
  return lines.join('\n');
}

export function fmtCounterexample(cx: Counterexample): string {
  const lines: string[] = [];
  lines.push(`Counterexample database (total rows: ${cx.totalRows}):`);
  lines.push(fmtDatabase(cx.db));
  lines.push('');
  lines.push(`Left query result (${cx.leftResult.length} tuples):`);
  lines.push(fmtTable(cx.leftResult, '  '));
  lines.push(`Right query result (${cx.rightResult.length} tuples):`);
  lines.push(fmtTable(cx.rightResult, '  '));
  lines.push('');
  lines.push('Differences:');
  for (const d of cx.differences) {
    const where = d.side === 'left' ? 'only in LEFT' : 'only in RIGHT';
    lines.push(`  ${fmtRow(d.tuple)}  ${where}`);
    lines.push('    caused by source rows:');
    for (const s of d.sources) {
      lines.push(`      ${s.relation}${fmtRow(s.row)}`);
    }
  }
  return lines.join('\n');
}

export function fmtSearchResult(result: SearchResult): string {
  if (result.equivalentWithinBound) {
    return [
      `No counterexample found: the two queries are equivalent on every database`,
      `with at most ${result.maxRows} total rows (${result.databasesExamined} databases examined).`,
    ].join('\n');
  }
  return [
    `Found a distinguishing database after examining ${result.databasesExamined} instances.`,
    fmtCounterexample(result.counterexample!),
  ].join('\n');
}
