import { createHash } from 'node:crypto';
import { KEYWORDS } from './lexer.ts';
import { bindProject, type BoundProject, type Decl } from './binder.ts';

export interface Edit {
  file: string;
  start: number;
  end: number;
  newText: string;
  // Set when the edit expands a shorthand import (`y` -> `z as y`): the local
  // alias token then lives at the END of newText, so a declaration whose token
  // starts at `start` must be remapped to `start + newText.length - localTokenLength`.
  localTokenLength?: number;
}

export interface Binding {
  refFile: string;
  refStart: number;
  declId: string | null;
}

export interface Patch {
  version: string;
  symbolId: string;
  oldName: string;
  newName: string;
  edits: Edit[];
  bindings: Binding[];
}

export type ConflictKind = 'captured' | 'shadowed' | 'rebound';

export interface Conflict {
  kind: ConflictKind;
  file: string;
  line: number;
  column: number;
  name: string;
  message: string;
}

export type PlanResult =
  | { ok: true; patch: Patch }
  | { ok: false; error?: string; conflicts: Conflict[] };

export type ApplyResult =
  | { ok: true; sources: Map<string, string>; conflicts: [] }
  | { ok: false; error?: string; conflicts: Conflict[] };

export function toProject(files: Record<string, string> | Map<string, string>): Map<string, string> {
  return files instanceof Map ? new Map(files) : new Map(Object.entries(files));
}

export function hashProject(sources: Map<string, string>): string {
  const h = createHash('sha1');
  for (const [file, content] of [...sources.entries()].sort()) {
    h.update(file);
    h.update('\0');
    h.update(content);
    h.update('\0');
  }
  return h.digest('hex');
}

function lineCol(src: string, pos: number): { line: number; column: number } {
  let line = 1;
  let last = 0;
  for (let i = 0; i < pos && i < src.length; i++) {
    if (src[i] === '\n') {
      line++;
      last = i + 1;
    }
  }
  return { line, column: pos - last + 1 };
}

function isShorthandImportDecl(bound: BoundProject, decl: Decl): boolean {
  const bf = bound.files.get(decl.file);
  if (!bf) return false;
  return bf.refs.some((r) => r.isImportSpecifier && r.shorthand && r.start === decl.start);
}

export function planRename(
  files: Record<string, string> | Map<string, string>,
  symbolId: string,
  newName: string,
): PlanResult {
  const sources = toProject(files);
  if (!/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(newName) || KEYWORDS.has(newName)) {
    return { ok: false, error: `invalid identifier for rename: ${JSON.stringify(newName)}`, conflicts: [] };
  }
  const bound = bindProject(sources);
  const decl = bound.decls.get(symbolId);
  if (!decl) return { ok: false, error: `unknown symbol: ${symbolId}`, conflicts: [] };
  const oldName = decl.name;

  const edits: Edit[] = [];
  const seen = new Set<string>();
  const pushEdit = (e: Edit): void => {
    const key = `${e.file}:${e.start}`;
    if (seen.has(key)) return;
    seen.add(key);
    edits.push(e);
  };

  if (newName !== oldName) {
    if (decl.kind === 'import' && isShorthandImportDecl(bound, decl)) {
      pushEdit({
        file: decl.file,
        start: decl.start,
        end: decl.end,
        newText: `${oldName} as ${newName}`,
        localTokenLength: newName.length,
      });
    } else {
      pushEdit({ file: decl.file, start: decl.start, end: decl.end, newText: newName });
    }
    for (const ref of bound.refs) {
      if (ref.target !== symbolId) continue;
      if (ref.isImportSpecifier && ref.shorthand) {
        pushEdit({
          file: ref.file,
          start: ref.start,
          end: ref.end,
          newText: `${newName} as ${ref.name}`,
          localTokenLength: ref.name.length,
        });
      } else {
        pushEdit({ file: ref.file, start: ref.start, end: ref.end, newText: newName });
      }
    }
  }

  const bindings: Binding[] = bound.refs.map((r) => ({
    refFile: r.file,
    refStart: r.start,
    declId: r.target,
  }));
  const patch: Patch = { version: hashProject(sources), symbolId, oldName, newName, edits, bindings };
  const conflicts = verifyAfterEdits(sources, patch);
  if (conflicts.length > 0) return { ok: false, conflicts };
  return { ok: true, patch };
}

export function applyEdits(sources: Map<string, string>, edits: Edit[]): Map<string, string> {
  const byFile = new Map<string, Edit[]>();
  for (const e of edits) {
    const list = byFile.get(e.file) ?? [];
    list.push(e);
    byFile.set(e.file, list);
  }
  const out = new Map(sources);
  for (const [file, list] of byFile) {
    let text = out.get(file);
    if (text === undefined) throw new Error(`edit targets unknown file: ${file}`);
    const sorted = [...list].sort((a, b) => b.start - a.start);
    for (const e of sorted) {
      text = text.slice(0, e.start) + e.newText + text.slice(e.end);
    }
    out.set(file, text);
  }
  return out;
}

function refOffsetMapper(edits: Edit[]): (pos: number) => number {
  const sorted = [...edits].sort((a, b) => a.start - b.start);
  return (pos: number): number => {
    let delta = 0;
    for (const e of sorted) {
      if (e.end <= pos) delta += e.newText.length - (e.end - e.start);
      else break;
    }
    return pos + delta;
  };
}

function declOffsetMapper(edits: Edit[]): (pos: number) => number {
  const sorted = [...edits].sort((a, b) => a.start - b.start);
  return (pos: number): number => {
    let delta = 0;
    for (const e of sorted) {
      if (e.end <= pos) {
        delta += e.newText.length - (e.end - e.start);
      } else if (e.start === pos && e.localTokenLength !== undefined) {
        delta += e.newText.length - e.localTokenLength;
      } else break;
    }
    return pos + delta;
  };
}

function makeConflict(
  before: BoundProject,
  kind: ConflictKind,
  b: Binding,
  message: string,
): Conflict {
  const src = before.sources.get(b.refFile) ?? '';
  const { line, column } = lineCol(src, b.refStart);
  const ref = before.refs.find((r) => r.file === b.refFile && r.start === b.refStart);
  return { kind, file: b.refFile, line, column, name: ref ? ref.name : '?', message };
}

export function verifyAfterEdits(sources: Map<string, string>, patch: Patch): Conflict[] {
  const before = bindProject(sources);
  const newSources = applyEdits(sources, patch.edits);
  const after = bindProject(newSources);

  const refMappers = new Map<string, (pos: number) => number>();
  const declMappers = new Map<string, (pos: number) => number>();
  for (const file of sources.keys()) {
    const fileEdits = patch.edits.filter((e) => e.file === file);
    refMappers.set(file, refOffsetMapper(fileEdits));
    declMappers.set(file, declOffsetMapper(fileEdits));
  }
  const mapId = (id: string): string => {
    const idx = id.lastIndexOf(':');
    const file = id.slice(0, idx);
    const pos = Number(id.slice(idx + 1));
    const mapper = declMappers.get(file);
    return `${file}:${mapper ? mapper(pos) : pos}`;
  };

  const afterRefs = new Map<string, string | null>();
  for (const r of after.refs) afterRefs.set(`${r.file}:${r.start}`, r.target);

  const renamedNewId = mapId(patch.symbolId);
  const conflicts: Conflict[] = [];
  for (const b of patch.bindings) {
    const mapper = refMappers.get(b.refFile);
    if (!mapper) continue;
    const key = `${b.refFile}:${mapper(b.refStart)}`;
    if (!afterRefs.has(key)) {
      conflicts.push(
        makeConflict(before, 'rebound', b, `reference '${b.refFile}:${b.refStart}' vanished after rename`),
      );
      continue;
    }
    const actual = afterRefs.get(key) ?? null;
    const expected = b.declId === null ? null : mapId(b.declId);
    if (actual === expected) continue;
    let kind: ConflictKind;
    let message: string;
    if (b.declId === patch.symbolId) {
      kind = 'shadowed';
      message = `reference to renamed symbol '${patch.newName}' would be shadowed by an intervening declaration`;
    } else if (actual === renamedNewId) {
      kind = 'captured';
      message = `renaming '${patch.oldName}' to '${patch.newName}' would capture this reference (currently bound to another symbol)`;
    } else {
      kind = 'rebound';
      message = `reference would change binding after renaming '${patch.oldName}' to '${patch.newName}'`;
    }
    conflicts.push(makeConflict(before, kind, b, message));
  }
  return conflicts;
}

export function applyPatch(
  files: Record<string, string> | Map<string, string>,
  patch: Patch,
): ApplyResult {
  const sources = toProject(files);
  if (hashProject(sources) !== patch.version) {
    return {
      ok: false,
      error: 'source version mismatch: patch was built against a different source snapshot',
      conflicts: [],
    };
  }
  const conflicts = verifyAfterEdits(sources, patch);
  if (conflicts.length > 0) return { ok: false, conflicts };
  return { ok: true, sources: applyEdits(sources, patch.edits), conflicts: [] };
}
