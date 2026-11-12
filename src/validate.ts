import { assertAllowedOrigin, parseHttpUrl } from './allowlist.ts';
import { ImportMapError } from './errors.ts';
import type { ImportMap, ValidatedImportMap, ValidatedScope } from './types.ts';

function validateMappings(
  entries: Record<string, string> | undefined,
  allowedOrigins: Set<string>,
  path: string,
): { key: string; targetHref: string; prefix: boolean }[] {
  const mappings: { key: string; targetHref: string; prefix: boolean }[] = [];
  if (entries === undefined) {
    return mappings;
  }

  for (const [key, rawTarget] of Object.entries(entries)) {
    const keyIsPrefix = key.endsWith('/');
    const targetUrl = parseHttpUrl(rawTarget, `target of "${key}" in ${path}`);
    assertAllowedOrigin(targetUrl, allowedOrigins, `target of "${key}" in ${path}`);

    if (keyIsPrefix && !rawTarget.endsWith('/')) {
      throw new ImportMapError(
        'PREFIX_MISMATCH',
        `prefix key "${key}" in ${path} must map to a target ending with "/"`,
      );
    }

    let keyUrl: URL | undefined;
    try {
      keyUrl = new URL(key);
    } catch {
      keyUrl = undefined;
    }

    let storedKey = key;
    if (keyUrl !== undefined) {
      if (keyUrl.protocol !== 'https:' && keyUrl.protocol !== 'http:') {
        throw new ImportMapError('INVALID_URL', `mapping key "${key}" in ${path} must use http or https`);
      }
      if (keyUrl.username !== '' || keyUrl.password !== '') {
        throw new ImportMapError('INVALID_URL', `mapping key "${key}" in ${path} must not contain credentials`);
      }
      assertAllowedOrigin(keyUrl, allowedOrigins, `mapping key "${key}" in ${path}`);
      storedKey = keyUrl.href;

      if (keyIsPrefix && !keyUrl.href.startsWith(keyUrl.origin + '/')) {
        throw new ImportMapError('INVALID_URL', `prefix key "${key}" in ${path} must be a path below its origin`);
      }
    }

    if (keyIsPrefix && !targetUrl.href.startsWith(targetUrl.origin + '/')) {
      throw new ImportMapError('PREFIX_ESCAPE', `prefix target for "${key}" in ${path} escapes its origin`);
    }

    mappings.push({ key: storedKey, targetHref: targetUrl.href, prefix: keyIsPrefix });
  }

  return mappings;
}

function normalizeScopePrefix(rawScope: string, allowedOrigins: Set<string>): string {
  const scopeUrl = parseHttpUrl(rawScope, 'scope key');
  assertAllowedOrigin(scopeUrl, allowedOrigins, 'scope key');

  const normalized = scopeUrl.href.endsWith('/') ? scopeUrl.href : `${scopeUrl.href}/`;
  if (!normalized.startsWith(scopeUrl.origin + '/')) {
    throw new ImportMapError('PREFIX_ESCAPE', `scope "${rawScope}" normalizes outside its origin`);
  }
  return normalized;
}

export function validateImportMap(map: ImportMap, allowedOrigins: Set<string>): ValidatedImportMap {
  const imports = validateMappings(map.imports, allowedOrigins, 'imports');

  const scopes: ValidatedScope[] = [];
  if (map.scopes !== undefined) {
    const scopePrefixes = new Set<string>();
    for (const [rawScope, entries] of Object.entries(map.scopes)) {
      const prefix = normalizeScopePrefix(rawScope, allowedOrigins);
      if (scopePrefixes.has(prefix)) {
        throw new ImportMapError('INVALID_SHAPE', `scope "${rawScope}" collides with another scope after trailing-slash normalization`);
      }
      scopePrefixes.add(prefix);
      const mappings = validateMappings(entries, allowedOrigins, `scopes["${rawScope}"]`);
      scopes.push({ prefix, mappings });
    }
    scopes.sort((a, b) => (a.prefix < b.prefix ? 1 : a.prefix > b.prefix ? -1 : 0));
  }

  return { imports, scopes };
}
