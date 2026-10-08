import { assemble, parseLine } from './parser.ts';
import type {
  Diagnostic,
  LineInfo,
  ParsedDocument,
  Reference,
} from './types.ts';

export type ReferenceChangeKind = 'added' | 'removed' | 'changed' | 'moved';

export interface ReferenceChange {
  key: string;
  name: string;
  kind: ReferenceChangeKind;
  /** Current reference (absent for `removed`). */
  reference?: Reference;
  /** Previous reference (absent for `added`). */
  previous?: Reference;
  /** Current diagnostics attached to this reference. */
  diagnostics: Diagnostic[];
}

export interface ChangedLineRange {
  /** First changed line (0-based, shared by old and new text). */
  start: number;
  /** Exclusive end of the changed region in the old text. */
  oldEnd: number;
  /** Exclusive end of the changed region in the new text. */
  newEnd: number;
}

export interface UpdateResult {
  affectedReferences: ReferenceChange[];
  /** Full diagnostic list after the update. */
  diagnostics: Diagnostic[];
  changedLines: ChangedLineRange;
}

function codesOf(doc: ParsedDocument, key: string): string {
  return (doc.diagnosticsByReference.get(key) ?? [])
    .map((d) => d.code)
    .sort()
    .join(',');
}

/** Identity of the definition a name resolves to (position-independent). */
function targetOf(doc: ParsedDocument, name: string): string {
  const definition = doc.definitionMap.get(name);
  return definition ? `${definition.name}=${definition.body}` : '';
}

/** Diff two parsed documents into the set of affected references. */
export function diffDocuments(
  oldDoc: ParsedDocument,
  newDoc: ParsedDocument,
): ReferenceChange[] {
  const changes: ReferenceChange[] = [];
  const oldByKey = new Map(oldDoc.references.map((r) => [r.key, r] as const));
  const newByKey = new Map(newDoc.references.map((r) => [r.key, r] as const));

  for (const [key, reference] of newByKey) {
    const previous = oldByKey.get(key);
    const diagnostics = newDoc.diagnosticsByReference.get(key) ?? [];
    if (!previous) {
      changes.push({ key, name: reference.name, kind: 'added', reference, diagnostics });
      continue;
    }
    const statusChanged =
      codesOf(oldDoc, key) !== codesOf(newDoc, key) ||
      targetOf(oldDoc, reference.name) !== targetOf(newDoc, reference.name);
    const moved =
      previous.line !== reference.line ||
      previous.range.start.character !== reference.range.start.character;
    if (statusChanged) {
      changes.push({ key, name: reference.name, kind: 'changed', reference, previous, diagnostics });
    } else if (moved) {
      changes.push({ key, name: reference.name, kind: 'moved', reference, previous, diagnostics });
    }
  }

  for (const [key, previous] of oldByKey) {
    if (!newByKey.has(key)) {
      changes.push({ key, name: previous.name, kind: 'removed', previous, diagnostics: [] });
    }
  }

  return changes.sort((a, b) => {
    const ra = (a.reference ?? a.previous)!;
    const rb = (b.reference ?? b.previous)!;
    return (
      ra.line - rb.line ||
      ra.range.start.character - rb.range.start.character ||
      a.key.localeCompare(b.key)
    );
  });
}

/**
 * A document that reparses only edited lines on update, reusing cached
 * per-line parses for unchanged lines, and reports the affected references.
 */
export class IncrementalDocument {
  private lines: string[];
  private lineCache: LineInfo[];
  private parsed: ParsedDocument;

  constructor(text: string) {
    this.lines = text.split('\n');
    this.lineCache = this.lines.map(parseLine);
    this.parsed = assemble(this.lineCache);
  }

  getDocument(): ParsedDocument {
    return this.parsed;
  }

  get headings() {
    return this.parsed.headings;
  }

  get references() {
    return this.parsed.references;
  }

  get definitions() {
    return this.parsed.definitions;
  }

  get diagnostics() {
    return this.parsed.diagnostics;
  }

  update(text: string): UpdateResult {
    const newLines = text.split('\n');
    const oldLines = this.lines;
    const minLength = Math.min(oldLines.length, newLines.length);

    let prefix = 0;
    while (prefix < minLength && oldLines[prefix] === newLines[prefix]) prefix++;
    let suffix = 0;
    while (
      suffix < minLength - prefix &&
      oldLines[oldLines.length - 1 - suffix] === newLines[newLines.length - 1 - suffix]
    ) {
      suffix++;
    }

    const newCache: LineInfo[] = new Array(newLines.length);
    for (let i = 0; i < prefix; i++) newCache[i] = this.lineCache[i];
    for (let i = 0; i < suffix; i++) {
      newCache[newLines.length - 1 - i] = this.lineCache[oldLines.length - 1 - i];
    }
    for (let i = prefix; i < newLines.length - suffix; i++) {
      newCache[i] = parseLine(newLines[i]);
    }

    const parsed = assemble(newCache);
    const affectedReferences = diffDocuments(this.parsed, parsed);
    const changedLines: ChangedLineRange = {
      start: prefix,
      oldEnd: oldLines.length - suffix,
      newEnd: newLines.length - suffix,
    };

    this.lines = newLines;
    this.lineCache = newCache;
    this.parsed = parsed;

    return { affectedReferences, diagnostics: parsed.diagnostics, changedLines };
  }
}
