import type { Program, Stmt, Expr } from './ast.ts';
import { parse } from './parser.ts';

export interface Scope {
  kind: 'module' | 'block' | 'fn';
  parent: Scope | null;
  decls: Decl[];
}

export type DeclKind = 'let' | 'fn' | 'param' | 'import';

export interface Decl {
  id: string;
  name: string;
  kind: DeclKind;
  file: string;
  start: number;
  end: number;
  declPos: number;
  scope: Scope;
  importedName?: string;
  moduleSpecifier?: string;
  resolvedModule?: string;
  exportTarget?: string | null;
}

export interface Ref {
  name: string;
  file: string;
  start: number;
  end: number;
  scope: Scope;
  target: string | null;
  isImportSpecifier?: boolean;
  shorthand?: boolean;
}

export interface BoundFile {
  file: string;
  program: Program;
  moduleScope: Scope;
  decls: Decl[];
  refs: Ref[];
  importRefs: { ref: Ref; alias: Decl }[];
}

export interface BoundProject {
  sources: Map<string, string>;
  files: Map<string, BoundFile>;
  decls: Map<string, Decl>;
  refs: Ref[];
}

export function resolve(scope: Scope, name: string, pos: number): Decl | null {
  let s: Scope | null = scope;
  while (s) {
    let best: Decl | null = null;
    for (const d of s.decls) {
      if (d.name !== name) continue;
      const hoisted = d.kind === 'fn' || d.kind === 'param';
      if (!hoisted && !(d.declPos < pos)) continue;
      if (!best || d.declPos >= best.declPos) best = d;
    }
    if (best) return best;
    s = s.parent;
  }
  return null;
}

export function bindFile(file: string, program: Program): BoundFile {
  const moduleScope: Scope = { kind: 'module', parent: null, decls: [] };
  const decls: Decl[] = [];
  const refs: Ref[] = [];
  const importRefs: { ref: Ref; alias: Decl }[] = [];

  function declare(
    scope: Scope,
    name: string,
    kind: DeclKind,
    start: number,
    end: number,
    declPos: number,
  ): Decl {
    const d: Decl = { id: `${file}:${start}`, name, kind, file, start, end, declPos, scope };
    scope.decls.push(d);
    decls.push(d);
    return d;
  }

  function walkStmts(stmts: Stmt[], scope: Scope): void {
    for (const s of stmts) walkStmt(s, scope);
  }

  function walkStmt(s: Stmt, scope: Scope): void {
    switch (s.kind) {
      case 'Let': {
        declare(scope, s.id.name, 'let', s.id.start, s.id.end, s.start);
        walkExpr(s.init, scope);
        break;
      }
      case 'Fn': {
        declare(scope, s.id.name, 'fn', s.id.start, s.id.end, s.start);
        const fnScope: Scope = { kind: 'fn', parent: scope, decls: [] };
        for (const p of s.params) declare(fnScope, p.name, 'param', p.start, p.end, s.start);
        walkStmts(s.body.body, fnScope);
        break;
      }
      case 'Import': {
        for (const spec of s.specs) {
          const shorthand = spec.imported.start === spec.local.start;
          const alias = declare(scope, spec.local.name, 'import', spec.local.start, spec.local.end, s.start);
          alias.importedName = spec.imported.name;
          alias.moduleSpecifier = s.source;
          const ref: Ref = {
            name: spec.imported.name,
            file,
            start: spec.imported.start,
            end: spec.imported.end,
            scope,
            target: null,
            isImportSpecifier: true,
            shorthand,
          };
          refs.push(ref);
          importRefs.push({ ref, alias });
        }
        break;
      }
      case 'Block': {
        const blockScope: Scope = { kind: 'block', parent: scope, decls: [] };
        walkStmts(s.body, blockScope);
        break;
      }
      case 'Return':
        walkExpr(s.arg, scope);
        break;
      case 'ExprStmt':
        walkExpr(s.expr, scope);
        break;
    }
  }

  function pushRef(name: string, start: number, end: number, scope: Scope): void {
    refs.push({ name, file, start, end, scope, target: null });
  }

  function walkExpr(e: Expr, scope: Scope): void {
    switch (e.kind) {
      case 'Ident':
        pushRef(e.name, e.start, e.end, scope);
        break;
      case 'Assign':
        pushRef(e.target.name, e.target.start, e.target.end, scope);
        walkExpr(e.value, scope);
        break;
      case 'Binary':
        walkExpr(e.left, scope);
        walkExpr(e.right, scope);
        break;
      case 'Call':
        pushRef(e.callee.name, e.callee.start, e.callee.end, scope);
        for (const a of e.args) walkExpr(a, scope);
        break;
      default:
        break;
    }
  }

  walkStmts(program.body, moduleScope);

  for (const r of refs) {
    if (!r.isImportSpecifier) {
      const d = resolve(r.scope, r.name, r.start);
      r.target = d ? d.id : null;
    }
  }
  return { file, program, moduleScope, decls, refs, importRefs };
}

export function resolveModulePath(fromFile: string, specifier: string): string {
  const fromDir = fromFile.split('/').slice(0, -1);
  const parts =
    specifier.startsWith('./') || specifier.startsWith('../')
      ? [...fromDir, ...specifier.split('/')]
      : specifier.split('/');
  const out: string[] = [];
  for (const p of parts) {
    if (p === '' || p === '.') continue;
    if (p === '..') out.pop();
    else out.push(p);
  }
  return out.join('/');
}

export function bindProject(sources: Map<string, string>): BoundProject {
  const files = new Map<string, BoundFile>();
  const decls = new Map<string, Decl>();
  const refs: Ref[] = [];
  for (const [file, src] of sources) {
    const bf = bindFile(file, parse(src, file));
    files.set(file, bf);
    for (const d of bf.decls) decls.set(d.id, d);
    for (const r of bf.refs) refs.push(r);
  }
  for (const bf of files.values()) {
    for (const { ref, alias } of bf.importRefs) {
      const target = resolveModulePath(bf.file, alias.moduleSpecifier as string);
      alias.resolvedModule = target;
      const tf = files.get(target);
      const exp = tf
        ? tf.decls.find((d) => d.scope.kind === 'module' && d.name === alias.importedName)
        : undefined;
      alias.exportTarget = exp ? exp.id : null;
      ref.target = alias.exportTarget;
    }
  }
  return { sources, files, decls, refs };
}
