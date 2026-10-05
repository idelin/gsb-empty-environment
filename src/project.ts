import { createHash } from 'node:crypto';
import { parse } from './parser.ts';
import { resolveFile, type Diagnostic, type FileAnalysis, type SymbolInfo } from './resolve.ts';

export interface Project {
  sources: Map<string, string>;
  version: string;
  files: Map<string, FileAnalysis>;
  symbols: Map<string, SymbolInfo>;
  diagnostics: Diagnostic[];
}

export function computeVersion(sources: Map<string, string>): string {
  const hash = createHash('sha256');
  for (const [file, content] of [...sources.entries()].sort()) {
    hash.update(file);
    hash.update('\0');
    hash.update(content);
    hash.update('\0');
  }
  return hash.digest('hex');
}

export function resolveModulePath(fromFile: string, spec: string): string {
  const segments = fromFile.split('/');
  segments.pop();
  for (const part of spec.split('/')) {
    if (part === '' || part === '.') continue;
    if (part === '..') segments.pop();
    else segments.push(part);
  }
  return segments.join('/');
}

export function analyzeProject(input: Record<string, string> | Map<string, string>): Project {
  const sources = input instanceof Map ? new Map(input) : new Map(Object.entries(input));
  const files = new Map<string, FileAnalysis>();
  for (const [file, src] of sources) {
    files.set(file, resolveFile(file, parse(src)));
  }

  for (const [file, fa] of files) {
    for (const spec of fa.importSpecs) {
      const targetFile = resolveModulePath(file, spec.source);
      const target = files.get(targetFile);
      const aliasSym = fa.symbols.find((s) => s.id === spec.aliasSymbolId);
      if (!target) {
        fa.diagnostics.push({
          file,
          code: 'import-unresolved',
          message: `cannot resolve module '${spec.source}' imported by '${file}'`,
          start: spec.sourceStart,
          end: spec.sourceEnd,
          nodeId: spec.importedIdent.id,
        });
        continue;
      }
      const exportedId = target.exports.get(spec.importedIdent.name);
      if (!exportedId) {
        fa.diagnostics.push({
          file,
          code: 'import-missing-export',
          message: `module '${targetFile}' has no exported symbol '${spec.importedIdent.name}'`,
          start: spec.importedIdent.start,
          end: spec.importedIdent.end,
          nodeId: spec.importedIdent.id,
        });
        continue;
      }
      fa.bindings.set(spec.importedIdent.id, exportedId);
      if (aliasSym) aliasSym.target = exportedId;
    }
  }

  const symbols = new Map<string, SymbolInfo>();
  const diagnostics: Diagnostic[] = [];
  for (const fa of files.values()) {
    for (const sym of fa.symbols) symbols.set(sym.id, sym);
    diagnostics.push(...fa.diagnostics);
  }

  return { sources, version: computeVersion(sources), files, symbols, diagnostics };
}
