import { digestValue } from "./canonical.ts";
import { confusableTableDigest } from "./confusables.ts";
import { scriptTableDigest } from "./scripts.ts";
import { suffixTableDigest } from "./suffix.ts";
import type {
  ConfusableTable,
  ScriptTable,
  SuffixTable,
  TableSummary,
  VersionedTables,
} from "./types.ts";

export function tableDigestsFor(
  tables: VersionedTables,
  version: string,
  suffixVersion: string,
): Readonly<Record<string, string>> {
  const confusables = tables.confusables[version];
  const scripts = tables.scripts[version];
  const suffixes = tables.suffixes[suffixVersion];
  if (!confusables) throw new Error(`missing confusable table '${version}'`);
  if (!scripts) throw new Error(`missing script table '${version}'`);
  if (!suffixes) throw new Error(`missing suffix table '${suffixVersion}'`);
  const confusablesDigest = confusableTableDigest(confusables);
  const scriptsDigest = scriptTableDigest(scripts);
  const suffixesDigest = suffixTableDigest(suffixes);
  return {
    confusables: confusablesDigest,
    scripts: scriptsDigest,
    suffixes: suffixesDigest,
    combined: digestValue({
      version,
      suffixVersion,
      confusables: confusablesDigest,
      scripts: scriptsDigest,
      suffixes: suffixesDigest,
    }),
  };
}

function sortedEntries<T>(
  record: Readonly<Record<string, T>>,
): Array<[string, T]> {
  return Object.keys(record)
    .sort()
    .map((key) => [key, record[key]] as [string, T]);
}

export function summarizeTables(tables: VersionedTables): TableSummary {
  const confusables: Record<
    string,
    { version: string; digest: string; ruleCount: number }
  > = {};
  for (const [key, table] of sortedEntries<ConfusableTable>(
    tables.confusables,
  )) {
    confusables[key] = {
      version: table.version,
      digest: confusableTableDigest(table),
      ruleCount: table.rules.length,
    };
  }
  const scripts: Record<
    string,
    { version: string; digest: string; rangeCount: number }
  > = {};
  for (const [key, table] of sortedEntries<ScriptTable>(tables.scripts)) {
    scripts[key] = {
      version: table.version,
      digest: scriptTableDigest(table),
      rangeCount: table.ranges.length,
    };
  }
  const suffixes: Record<
    string,
    { version: string; digest: string; ruleCount: number }
  > = {};
  for (const [key, table] of sortedEntries<SuffixTable>(tables.suffixes)) {
    suffixes[key] = {
      version: table.version,
      digest: suffixTableDigest(table),
      ruleCount: table.rules.length,
    };
  }
  return {
    confusables,
    scripts,
    suffixes,
    combinedDigest: digestValue({ confusables, scripts, suffixes }),
  };
}
