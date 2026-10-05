export type {
  Position,
  SourceRange,
  OldStatement,
  NewStatement,
  OldCoverageFile,
  NewStatementsFile,
} from './types.ts';
export { normalizeSource, fingerprintOf } from './fingerprint.ts';
export { validateRules } from './rules.ts';
export type { MappingRule, RulesFile, RuleRejection } from './rules.ts';
export { migrateCoverage } from './migrate.ts';
export type {
  MigrationReport,
  MigrationSummary,
  InheritedEntry,
  AddedEntry,
  UncertainEntry,
  MigratedStatement,
  Provenance,
  InheritVia,
} from './migrate.ts';
export { formatTextReport } from './report.ts';
