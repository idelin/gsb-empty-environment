import { KEYWORDS } from './lexer.ts';
import { analyzeProject, type Project } from './project.ts';
import type { Ident } from './ast.ts';

export interface RenameEdit {
  file: string;
  start: number;
  end: number;
  newText: string;
}

export interface RenamePatch {
  baseVersion: string;
  symbolId: string;
  newName: string;
  edits: RenameEdit[];
}

export type ConflictReason =
  | 'capture'
  | 'shadow'
  | 'captured-unbound'
  | 'lost-binding'
  | 'rebind'
  | 'duplicate'
  | 'unbound'
  | 'import-unresolved'
  | 'import-missing-export';

export interface Conflict {
  file: string;
  start: number;
  end: number;
  reason: ConflictReason;
  message: string;
}

export interface PreflightResult {
  ok: boolean;
  error?: string;
  edits: RenameEdit[];
  conflicts: Conflict[];
  patch?: RenamePatch;
}

export type ApplyResult =
  | { ok: true; version: string; sources: Map<string, string>; project: Project }
  | { ok: false; error: string; conflicts?: Conflict[] };

const IDENT_RE = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

interface RenamePlan {
  edits: RenameEdit[];
  renamedIds: Set<string>;
}

function planRename(project: Project, symbolId: string, newName: string): RenamePlan | { error: string } {
  const sym = project.symbols.get(symbolId);
  if (!sym) return { error: `symbol not found: ${symbolId}` };

  const renamedIds = new Set<string>([sym.id]);
  const occurrences = new Map<string, { file: string; ident: Ident }>();
  const add = (file: string, ident: Ident) =>
    occurrences.set(`${file}:${ident.id}`, { file, ident });
  const addRefsTo = (id: string) => {
    for (const [file, fa] of project.files) {
      for (const [identId, target] of fa.bindings) {
        if (target === id) add(file, fa.program.idents[identId]);
      }
    }
  };

  add(sym.file, sym.nameIdent);
  addRefsTo(sym.id);

  if (sym.exported) {
    for (const fa of project.files.values()) {
      for (const spec of fa.importSpecs) {
        if (spec.localIdent !== null) continue;
        const alias = project.symbols.get(spec.aliasSymbolId);
        if (alias && alias.target === sym.id && !renamedIds.has(alias.id)) {
          renamedIds.add(alias.id);
          addRefsTo(alias.id);
        }
      }
    }
  }

  const edits = [...occurrences.values()]
    .map(({ file, ident }) => ({ file, start: ident.start, end: ident.end, newText: newName }))
    .sort((a, b) => (a.file < b.file ? -1 : a.file > b.file ? 1 : a.start - b.start));
  return { edits, renamedIds };
}

export function applyEdits(sources: Map<string, string>, edits: RenameEdit[]): Map<string, string> {
  const byFile = new Map<string, RenameEdit[]>();
  for (const edit of edits) {
    const list = byFile.get(edit.file) ?? [];
    list.push(edit);
    byFile.set(edit.file, list);
  }
  const out = new Map(sources);
  for (const [file, list] of byFile) {
    let text = out.get(file);
    if (text === undefined) throw new Error(`edit targets unknown file: ${file}`);
    const sorted = [...list].sort((a, b) => b.start - a.start);
    let lastStart = Infinity;
    for (const edit of sorted) {
      if (edit.end > lastStart) throw new Error(`overlapping edits in ${file} at ${edit.start}`);
      text = text.slice(0, edit.start) + edit.newText + text.slice(edit.end);
      lastStart = edit.start;
    }
    out.set(file, text);
  }
  return out;
}

function compareProjects(oldP: Project, newP: Project, renamedIds: Set<string>): Conflict[] {
  const conflicts: Conflict[] = [];
  const nameOf = (id: string | undefined) =>
    id === undefined ? '<unbound>' : (oldP.symbols.get(id)?.name ?? id);

  for (const [file, oldFa] of oldP.files) {
    const newFa = newP.files.get(file);
    if (!newFa) continue;

    const identIds = new Set([...oldFa.bindings.keys(), ...newFa.bindings.keys()]);
    for (const identId of identIds) {
      const before = oldFa.bindings.get(identId);
      const after = newFa.bindings.get(identId);
      if (before === after) continue;
      const ident = oldFa.program.idents[identId];
      let reason: ConflictReason;
      let message: string;
      if (before === undefined) {
        reason = 'captured-unbound';
        message = `previously unbound reference '${ident.name}' would bind to '${nameOf(after)}'`;
      } else if (renamedIds.has(before)) {
        if (after === undefined) {
          reason = 'lost-binding';
          message = `reference to the renamed symbol would become unbound`;
        } else {
          reason = 'capture';
          message = `reference to the renamed symbol would be captured by '${nameOf(after)}'`;
        }
      } else if (after !== undefined && renamedIds.has(after)) {
        reason = 'shadow';
        message = `reference to '${nameOf(before)}' would be shadowed by the renamed declaration`;
      } else {
        reason = 'rebind';
        message = `reference would rebind from '${nameOf(before)}' to '${nameOf(after)}'`;
      }
      conflicts.push({ file, start: ident.start, end: ident.end, reason, message });
    }

    const oldDiagKeys = new Set(oldFa.diagnostics.map((d) => `${d.code}:${d.nodeId}`));
    for (const diag of newFa.diagnostics) {
      if (oldDiagKeys.has(`${diag.code}:${diag.nodeId}`)) continue;
      const ident = oldFa.program.idents[diag.nodeId];
      conflicts.push({
        file,
        start: ident ? ident.start : diag.start,
        end: ident ? ident.end : diag.end,
        reason: diag.code,
        message: diag.message,
      });
    }
  }

  conflicts.sort((a, b) =>
    a.file < b.file ? -1 : a.file > b.file ? 1 : a.start - b.start,
  );
  return conflicts;
}

export function preflightRename(
  project: Project,
  symbolId: string,
  newName: string,
): PreflightResult {
  if (!IDENT_RE.test(newName) || KEYWORDS.has(newName)) {
    return { ok: false, error: `invalid identifier: '${newName}'`, edits: [], conflicts: [] };
  }
  const planned = planRename(project, symbolId, newName);
  if ('error' in planned) return { ok: false, error: planned.error, edits: [], conflicts: [] };

  const { edits, renamedIds } = planned;
  let newProject: Project;
  try {
    newProject = analyzeProject(applyEdits(project.sources, edits));
  } catch (err) {
    return {
      ok: false,
      error: `renamed source fails to parse: ${(err as Error).message}`,
      edits,
      conflicts: [],
    };
  }

  const conflicts = compareProjects(project, newProject, renamedIds);
  const patch: RenamePatch = { baseVersion: project.version, symbolId, newName, edits };
  return { ok: conflicts.length === 0, edits, conflicts, patch };
}

export function applyRenamePatch(project: Project, patch: RenamePatch): ApplyResult {
  if (patch.baseVersion !== project.version) {
    return {
      ok: false,
      error:
        `stale patch: base version ${patch.baseVersion.slice(0, 12)}… ` +
        `does not match current source version ${project.version.slice(0, 12)}…`,
    };
  }
  const planned = planRename(project, patch.symbolId, patch.newName);
  if ('error' in planned) return { ok: false, error: planned.error };

  const newSources = applyEdits(project.sources, patch.edits);
  let newProject: Project;
  try {
    newProject = analyzeProject(newSources);
  } catch (err) {
    return { ok: false, error: `patched source fails to parse: ${(err as Error).message}` };
  }

  const conflicts = compareProjects(project, newProject, planned.renamedIds);
  if (conflicts.length > 0) {
    return { ok: false, error: 'post-apply verification failed', conflicts };
  }
  return { ok: true, version: newProject.version, sources: newSources, project: newProject };
}
