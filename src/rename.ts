import type { Ident, Program, Span } from "./ast.ts";
import { parse } from "./parser.ts";
import { bind, resolveName, type Binding, type BindResult } from "./binder.ts";
import { computePaths, verifyIsomorphism } from "./verify.ts";
import { KEYWORDS } from "./lexer.ts";

export interface TextEdit {
  span: Span;
  text: string;
}

export type RenameResult =
  | { ok: true; source: string; edits: TextEdit[] }
  | { ok: false; reason: string };

const IDENT_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

function lineCol(source: string, offset: number): string {
  let line = 1;
  let col = 1;
  for (let i = 0; i < offset && i < source.length; i++) {
    if (source[i] === "\n") {
      line++;
      col = 1;
    } else {
      col++;
    }
  }
  return `${line}:${col}`;
}

function containsOffset(span: Span, offset: number): boolean {
  return span.start <= offset && offset <= span.end;
}

function findBindingAt(program: Program, bound: BindResult, offset: number): Binding | null {
  for (const b of bound.bindings) {
    if (containsOffset(b.nameSpan, offset)) return b;
  }
  for (const [ident, target] of bound.resolutions) {
    if (containsOffset(ident.span, offset)) return target;
  }
  return null;
}

function applyEdits(source: string, edits: TextEdit[]): string {
  const sorted = [...edits].sort((a, b) => b.span.start - a.span.start);
  let out = source;
  for (const edit of sorted) {
    out = out.slice(0, edit.span.start) + edit.text + out.slice(edit.span.end);
  }
  return out;
}

/**
 * Renames the binding found at `offset` (on its declaration or on any of its
 * references) to `newName`. Only the declaration and the references that
 * resolve to that exact binding are edited; same-spelled identifiers owned
 * by other scopes are left untouched.
 *
 * A rename is rejected when the new name would change how any identifier in
 * the program resolves: either one of the binding's own references would be
 * captured by an intervening `newName` binding, or an existing `newName`
 * occurrence (bound or unbound) would be captured by the renamed binding.
 * After editing, the new source is re-parsed and re-bound, and a
 * binding-graph isomorphism check verifies that resolution is unchanged.
 */
export function rename(source: string, offset: number, newName: string): RenameResult {
  if (!IDENT_RE.test(newName) || KEYWORDS.has(newName)) {
    return { ok: false, reason: `'${newName}' is not a valid identifier` };
  }

  let program: Program;
  try {
    program = parse(source);
  } catch (err) {
    return { ok: false, reason: `cannot rename in unparseable source: ${(err as Error).message}` };
  }
  const bound = bind(program);

  const target = findBindingAt(program, bound, offset);
  if (!target) {
    return { ok: false, reason: `no binding declared or referenced at offset ${offset}` };
  }
  if (newName === target.name) {
    return { ok: true, source, edits: [] };
  }

  const refs: Ident[] = [];
  for (const [ident, resolved] of bound.resolutions) {
    if (resolved === target) refs.push(ident);
  }

  // The binding's own references must still resolve to it under the new name;
  // otherwise an intervening `newName` binding would capture them.
  for (const ref of refs) {
    const scope = bound.identScopes.get(ref)!;
    const winner = resolveName(scope, ref.span.start, newName, target, newName);
    if (winner !== target) {
      const where = lineCol(source, ref.span.start);
      const capturer = winner
        ? `binding '${winner.name}' declared at ${lineCol(source, winner.nameSpan.start)}`
        : "no binding";
      return {
        ok: false,
        reason:
          `renaming '${target.name}' to '${newName}' would change resolution of the ` +
          `reference at ${where}: it would be captured by ${capturer}`,
      };
    }
  }

  // Existing occurrences spelled `newName` (bound to something else, or
  // unbound) must not start resolving to the renamed binding.
  for (const [ident, resolved] of bound.resolutions) {
    if (ident.text !== newName || resolved === target) continue;
    const scope = bound.identScopes.get(ident)!;
    const winner = resolveName(scope, ident.span.start, newName, target, newName);
    if (winner === target) {
      const where = lineCol(source, ident.span.start);
      const before = resolved
        ? `binding '${resolved.name}' declared at ${lineCol(source, resolved.nameSpan.start)}`
        : "unbound";
      return {
        ok: false,
        reason:
          `renaming '${target.name}' to '${newName}' would capture the reference at ${where} ` +
          `(currently ${before})`,
      };
    }
  }

  const edits: TextEdit[] = [
    { span: target.nameSpan, text: newName },
    ...refs.map((ref) => ({ span: ref.span, text: newName })),
  ];
  const newSource = applyEdits(source, edits);

  // Verification: re-parse, re-bind, and require the binding graphs to be
  // isomorphic with edits confined to the intended nodes.
  const oldPaths = computePaths(program);
  const editedPaths = new Set<string>();
  editedPaths.add(oldPaths.get(target.decl)!);
  for (const ref of refs) editedPaths.add(oldPaths.get(ref)!);

  const newProgram = parse(newSource);
  const newBound = bind(newProgram);
  const problem = verifyIsomorphism(program, bound, newProgram, newBound, editedPaths, newName);
  if (problem) {
    return { ok: false, reason: `rename rejected by binding-graph verification: ${problem}` };
  }

  return { ok: true, source: newSource, edits };
}
