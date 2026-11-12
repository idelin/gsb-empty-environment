import { parseAllowedOrigins, parseHttpUrl } from './allowlist.ts';
import { ImportMapError } from './errors.ts';
import { parseImportMapText, validateImportMapShape } from './parse.ts';
import type {
  ImportMap,
  ImportMapGuardOptions,
  ValidatedImportMap,
  ValidatedScope,
} from './types.ts';
import { validateImportMap } from './validate.ts';

export class ImportMapGuard {
  private readonly allowedOrigins: Set<string>;
  private validated: ValidatedImportMap | null = null;
  private locked = false;

  constructor(options: ImportMapGuardOptions) {
    this.allowedOrigins = parseAllowedOrigins(options.allowedOrigins);
  }

  register(mapInput: string | ImportMap): ValidatedImportMap {
    if (this.validated !== null) {
      throw new ImportMapError('SECOND_MAP', 'a second import map is not allowed; exactly one map may be registered');
    }
    if (this.locked) {
      throw new ImportMapError('GUARD_LOCKED', 'import map registration is closed after module resolution started');
    }

    const map = typeof mapInput === 'string' ? parseImportMapText(mapInput) : validateImportMapShape(mapInput);
    this.validated = validateImportMap(map, this.allowedOrigins);
    return this.validated;
  }

  lock(): void {
    this.locked = true;
  }

  hasMap(): boolean {
    return this.validated !== null;
  }

  resolve(specifier: string, importer?: string): string {
    if (this.validated === null) {
      throw new ImportMapError('NO_MATCH', 'no import map has been registered');
    }
    this.lock();

    const scope = this.selectScope(importer);

    const scopedMatch = scope ? this.matchMapping(scope.mappings, specifier) : undefined;
    if (scopedMatch !== undefined) {
      return scopedMatch;
    }

    const topLevelMatch = this.matchMapping(this.validated.imports, specifier);
    if (topLevelMatch !== undefined) {
      return topLevelMatch;
    }

    let absolute: URL;
    try {
      absolute = new URL(specifier);
    } catch {
      throw new ImportMapError('NO_MATCH', `no mapping found for specifier "${specifier}"`);
    }

    if (absolute.protocol !== 'https:' && absolute.protocol !== 'http:') {
      throw new ImportMapError('NO_MATCH', `specifier "${specifier}" does not use http or https`);
    }
    if (!this.allowedOrigins.has(absolute.origin)) {
      throw new ImportMapError('DISALLOWED_ORIGIN', `specifier "${specifier}" resolves to an origin outside the allow list`);
    }

    return absolute.href;
  }

  private selectScope(importer: string | undefined): ValidatedScope | undefined {
    if (importer === undefined || this.validated === null) {
      return undefined;
    }

    const importerUrl = parseHttpUrl(importer, 'importer');
    if (!this.allowedOrigins.has(importerUrl.origin)) {
      throw new ImportMapError('DISALLOWED_ORIGIN', `importer "${importer}" is outside the allow list`);
    }

    return this.validated.scopes.find((scope) => importerUrl.href.startsWith(scope.prefix));
  }

  private matchMapping(
    mappings: readonly { key: string; targetHref: string; prefix: boolean }[],
    specifier: string,
  ): string | undefined {
    let specifierHref: string | undefined;
    try {
      specifierHref = new URL(specifier).href;
    } catch {
      return this.matchBare(mappings, specifier);
    }

    const exact = mappings.find((mapping) => !mapping.prefix && mapping.key === specifierHref);
    if (exact !== undefined) {
      return exact.targetHref;
    }

    const prefix = mappings
      .filter((mapping) => mapping.prefix && specifierHref.startsWith(mapping.key))
      .sort((a, b) => b.key.length - a.key.length)[0];

    if (prefix === undefined) {
      return undefined;
    }

    const resolved = prefix.targetHref + specifierHref.slice(prefix.key.length);
    const resolvedUrl = parseHttpUrl(resolved, 'resolved module URL');
    if (!this.allowedOrigins.has(resolvedUrl.origin)) {
      throw new ImportMapError('DISALLOWED_ORIGIN', `resolved module "${resolved}" is outside the allow list`);
    }
    if (!resolvedUrl.href.startsWith(prefix.targetHref)) {
      throw new ImportMapError('PREFIX_ESCAPE', `prefix resolution of "${specifier}" escaped "${prefix.targetHref}"`);
    }

    return resolved;
  }

  private matchBare(
    mappings: readonly { key: string; targetHref: string; prefix: boolean }[],
    specifier: string,
  ): string | undefined {
    const exact = mappings.find((mapping) => !mapping.prefix && mapping.key === specifier);
    if (exact !== undefined) {
      return exact.targetHref;
    }

    const prefix = mappings
      .filter((mapping) => mapping.prefix && specifier.startsWith(mapping.key))
      .sort((a, b) => b.key.length - a.key.length)[0];

    if (prefix === undefined) {
      return undefined;
    }

    const resolved = prefix.targetHref + specifier.slice(prefix.key.length);
    const resolvedUrl = parseHttpUrl(resolved, 'resolved module URL');
    if (!this.allowedOrigins.has(resolvedUrl.origin)) {
      throw new ImportMapError('DISALLOWED_ORIGIN', `resolved module "${resolved}" is outside the allow list`);
    }
    if (!resolvedUrl.href.startsWith(prefix.targetHref)) {
      throw new ImportMapError('PREFIX_ESCAPE', `prefix resolution of "${specifier}" escaped "${prefix.targetHref}"`);
    }

    return resolved;
  }
}
