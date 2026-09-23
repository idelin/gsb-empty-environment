function parsePath(path: string): string[] {
  if (!path) throw new Error('path must not be empty');
  return path.split('.');
}

export function getPath(root: unknown, path: string): unknown {
  let node = root;
  for (const segment of parsePath(path)) {
    if (node === null || typeof node !== 'object') return undefined;
    node = (node as Record<string, unknown>)[segment];
  }
  return node;
}

export function setPath(
  root: Record<string, unknown>,
  path: string,
  value: unknown,
): void {
  const segments = parsePath(path);
  let node = root;
  for (let index = 0; index < segments.length - 1; index += 1) {
    const next = node[segments[index]];
    if (next === null || typeof next !== 'object' || Array.isArray(next)) {
      node[segments[index]] = {};
    }
    node = node[segments[index]] as Record<string, unknown>;
  }
  node[segments[segments.length - 1]] = value;
}

export function deletePath(root: Record<string, unknown>, path: string): boolean {
  const segments = parsePath(path);
  let node: unknown = root;
  for (let index = 0; index < segments.length - 1; index += 1) {
    if (node === null || typeof node !== 'object') return false;
    node = (node as Record<string, unknown>)[segments[index]];
  }
  if (node === null || typeof node !== 'object') return false;
  return delete (node as Record<string, unknown>)[segments[segments.length - 1]];
}
