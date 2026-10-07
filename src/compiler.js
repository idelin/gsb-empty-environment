'use strict';

const { sha256 } = require('./hash');

const DEFAULT_COMPILER_OPTIONS = {
  strict: true,
  declaration: true,
  noEmitOnError: false,
  skipLibCheck: true,
  types: [],
};

function resolveCompilerOptions(ts, overrides) {
  const base = {
    target: ts.ScriptTarget.ES2020,
    module: ts.ModuleKind.CommonJS,
  };
  return Object.assign(base, DEFAULT_COMPILER_OPTIONS, overrides || {});
}

function formatDiagnostic(ts, diagnostic) {
  const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n');
  if (diagnostic.file && typeof diagnostic.start === 'number') {
    const pos = diagnostic.file.getLineAndCharacterOfPosition(diagnostic.start);
    return `${diagnostic.file.fileName}:${pos.line + 1}:${pos.character + 1} - TS${diagnostic.code}: ${message}`;
  }
  return `TS${diagnostic.code}: ${message}`;
}

/**
 * Compile one group of modules (a single file or an SCC) to JS + .d.ts.
 *
 * A full program over `allFiles` is created so cross-module types resolve
 * against current sources, but only the group's files are checked and emitted.
 *
 * Returns { ok: true, members: Map<absPath, {js, dts, signature}> } or
 * { ok: false, errors: string[] }. Nothing is produced on failure.
 */
function compileGroup(ts, allFiles, groupFiles, compilerOptions) {
  const options = resolveCompilerOptions(ts, compilerOptions);
  const program = ts.createProgram(allFiles, options);

  const errors = [];
  for (const file of groupFiles) {
    const sourceFile = program.getSourceFile(file);
    if (!sourceFile) {
      errors.push(`internal error: ${file} not part of the program`);
      continue;
    }
    for (const diagnostic of ts.getPreEmitDiagnostics(program, sourceFile)) {
      if (diagnostic.category === ts.DiagnosticCategory.Error) {
        errors.push(formatDiagnostic(ts, diagnostic));
      }
    }
  }
  if (errors.length > 0) {
    return { ok: false, errors };
  }

  const members = new Map();
  for (const file of groupFiles) {
    const sourceFile = program.getSourceFile(file);
    const emitted = {};
    const result = program.emit(sourceFile, (name, data) => {
      emitted[name] = data;
    });
    if (result.emitSkipped) {
      return { ok: false, errors: [`emit skipped for ${file}`] };
    }
    let js;
    let dts;
    for (const name of Object.keys(emitted)) {
      if (name.endsWith('.d.ts')) dts = emitted[name];
      else if (name.endsWith('.js')) js = emitted[name];
    }
    if (typeof js !== 'string' || typeof dts !== 'string') {
      return { ok: false, errors: [`missing js or d.ts output for ${file}`] };
    }
    members.set(file, { js, dts, signature: sha256(dts) });
  }
  return { ok: true, members };
}

module.exports = { compileGroup, resolveCompilerOptions };
