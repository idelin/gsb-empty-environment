import { parse } from './parser.ts';
import { bind } from './binder.ts';
import type { BindingGraph } from './binder.ts';
import { checkIsomorphic } from './verify.ts';

export interface TextEdit {
  start: number;
  end: number;
  text: string;
}

export type RenameResult =
  | { ok: true; newSource: string; edits: TextEdit[] }
  | { ok: false; reason: string };

const IDENT_RE = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const KEYWORDS = new Set(['let']);

function applyEdits(source: string, edits: TextEdit[]): string {
  const sorted = [...edits].sort((a, b) => b.start - a.start);
  let out = source;
  for (const edit of sorted) {
    out = out.slice(0, edit.start) + edit.text + out.slice(edit.end);
  }
  return out;
}

interface Target {
  declId: number;
  oldName: string;
}

function findTarget(graph: BindingGraph, offset: number): Target | { ok: false; reason: string } {
  for (const decl of graph.decls) {
    if (decl.nameSpan.start <= offset && offset < decl.nameSpan.end) {
      return { declId: decl.id, oldName: decl.name };
    }
  }
  for (const ref of graph.refs) {
    if (ref.span.start <= offset && offset < ref.span.end) {
      if (ref.declId === null) {
        return {
          ok: false,
          reason: `cannot rename unbound reference '${ref.name}' at offset ${ref.span.start}`,
        };
      }
      return { declId: ref.declId, oldName: ref.name };
    }
  }
  return { ok: false, reason: `no identifier at offset ${offset}` };
}

// Renames every occurrence (declaration and bound references) of the binding
// identified by `offset`. The rename is rejected when the new name would
// change how any reference in the program resolves — i.e. when renamed
// references would be captured by another binding of `newName`, or when the
// renamed declaration would capture or shadow existing references. The check
// is performed by reparsing the renamed source and verifying that the new
// binding graph is isomorphic to the original one.
export function rename(source: string, offset: number, newName: string): RenameResult {
  if (!IDENT_RE.test(newName) || KEYWORDS.has(newName)) {
    return { ok: false, reason: `'${newName}' is not a valid identifier` };
  }

  const graph = bind(parse(source));
  const target = findTarget(graph, offset);
  if ('ok' in target) return target;

  if (newName === target.oldName) {
    return { ok: true, newSource: source, edits: [] };
  }

  const decl = graph.decls[target.declId];
  const edits: TextEdit[] = [
    { start: decl.nameSpan.start, end: decl.nameSpan.end, text: newName },
  ];
  const renamedRefIds = new Set<number>();
  for (const ref of graph.refs) {
    if (ref.declId === target.declId) {
      renamedRefIds.add(ref.id);
      edits.push({ start: ref.span.start, end: ref.span.end, text: newName });
    }
  }

  const newSource = applyEdits(source, edits);

  // Verification: reparse and require an isomorphic binding graph.
  const newGraph = bind(parse(newSource));
  const check = checkIsomorphic(graph, newGraph);
  if (!check.ok) {
    const moved = newGraph.refs[check.refId];
    if (renamedRefIds.has(check.refId)) {
      return {
        ok: false,
        reason:
          `renaming to '${newName}' would capture the reference at offset ` +
          `${moved.span.start}: it would resolve to a different declaration of '${newName}'`,
      };
    }
    return {
      ok: false,
      reason:
        `renaming to '${newName}' would change the resolution of the existing reference ` +
        `'${moved.name}' at offset ${moved.span.start}`,
    };
  }

  return { ok: true, newSource, edits };
}
