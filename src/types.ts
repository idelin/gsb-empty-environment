export interface SourcePosition {
  line: number;
  column: number;
}

export interface SourceRange {
  start: SourcePosition;
  end: SourcePosition;
}

export interface OldStatement {
  id: string;
  file: string;
  range: SourceRange;
  text?: string;
  hits: number;
}

export interface NewStatement {
  id: string;
  file: string;
  range: SourceRange;
  text?: string;
}

export interface OldCoverageData {
  statements: OldStatement[];
}

export interface NewCoverageModel {
  statements: NewStatement[];
}

export interface MoveRule {
  kind: "move";
  from: string;
  to: string;
  reason?: string;
}

export interface SplitRule {
  kind: "split";
  from: string;
  to: string[];
  reason?: string;
}

export interface MergeRule {
  kind: "merge";
  from: string[];
  to: string;
  reason?: string;
}

export type MappingRule = MoveRule | SplitRule | MergeRule;

export interface CoverageSource {
  oldId: string;
  file: string;
  range: SourceRange;
  hits: number;
}

export interface InheritedCoverage {
  newId: string;
  file: string;
  range: SourceRange;
  provenance: string[];
  sources: CoverageSource[];
}

export interface AddedStatement {
  newId: string;
  file: string;
  range: SourceRange;
}

export interface UncertainItem {
  oldId: string;
  file: string;
  range: SourceRange;
  hits: number;
  reason: string;
  candidates: string[];
}

export interface MigrationSummary {
  oldTotal: number;
  newTotal: number;
  inherited: number;
  added: number;
  uncertain: number;
}

export interface MigrationReport {
  inherited: InheritedCoverage[];
  added: AddedStatement[];
  uncertain: UncertainItem[];
  diagnostics: string[];
  summary: MigrationSummary;
}
