export interface AccessPath {
  base: string;
  fields: string[];
}

export type Stmt =
  | { kind: "source"; id: string; target: AccessPath }
  | { kind: "assign"; target: AccessPath; from: AccessPath }
  | { kind: "sanitize"; target: AccessPath }
  | { kind: "call"; site: string; callee: string; args: AccessPath[]; results: string[] }
  | { kind: "return"; values: AccessPath[] }
  | { kind: "sink"; id: string; args: AccessPath[] };

export interface FunctionDecl {
  name: string;
  params: string[];
  body: Stmt[];
}

export interface Program {
  entry: string;
  functions: FunctionDecl[];
}

export function ap(base: string, ...fields: string[]): AccessPath {
  return { base, fields };
}

export function validateProgram(program: Program): void {
  if (!program || !Array.isArray(program.functions)) {
    throw new Error("program.functions must be an array");
  }
  const byName = new Map<string, FunctionDecl>();
  for (const fn of program.functions) {
    if (byName.has(fn.name)) throw new Error(`duplicate function: ${fn.name}`);
    byName.set(fn.name, fn);
  }
  if (!byName.has(program.entry)) {
    throw new Error(`entry function not found: ${program.entry}`);
  }
  for (const fn of program.functions) {
    for (const stmt of fn.body) {
      if (stmt.kind === "call") {
        const callee = byName.get(stmt.callee);
        if (!callee) {
          throw new Error(`call ${stmt.site} in ${fn.name}: unknown callee ${stmt.callee}`);
        }
        if (stmt.args.length > callee.params.length) {
          throw new Error(
            `call ${stmt.site} in ${fn.name}: ${stmt.args.length} args but ${stmt.callee} takes ${callee.params.length}`,
          );
        }
      }
    }
  }
}
