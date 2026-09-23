export type Path = ReadonlyArray<string | number>;

export type Op =
  | { readonly type: 'set'; readonly path: Path; readonly value: unknown }
  | { readonly type: 'unset'; readonly path: Path }
  | { readonly type: 'merge'; readonly path: Path; readonly value: Record<string, unknown> };

export function getIn(root: unknown, path: Path): unknown {
  let node = root;
  for (const key of path) {
    if (node == null) return undefined;
    node = (node as Record<string | number, unknown>)[key];
  }
  return node;
}

export function setIn(root: unknown, path: Path, value: unknown): unknown {
  if (path.length === 0) return value;
  const [key, ...rest] = path;
  const base = root ?? (typeof key === 'number' ? [] : {});
  const child = (base as Record<string | number, unknown>)[key];
  const nextChild = setIn(child, rest, value);
  if (Object.is(child, nextChild)) return root;
  const copy: Record<string | number, unknown> = Array.isArray(base)
    ? ((base as unknown[]).slice() as unknown as Record<string | number, unknown>)
    : { ...(base as object) };
  copy[key] = nextChild;
  return copy;
}

export function unsetIn(root: unknown, path: Path): unknown {
  if (path.length === 0) return undefined;
  if (root == null) return root;
  const [key, ...rest] = path;
  if (rest.length === 0) {
    if (!(key in (root as object))) return root;
    const copy: Record<string | number, unknown> = Array.isArray(root)
      ? ((root as unknown[]).slice() as unknown as Record<string | number, unknown>)
      : { ...(root as object) };
    delete copy[key];
    return copy;
  }
  const child = (root as Record<string | number, unknown>)[key];
  const nextChild = unsetIn(child, rest);
  if (Object.is(child, nextChild)) return root;
  const copy: Record<string | number, unknown> = Array.isArray(root)
    ? ((root as unknown[]).slice() as unknown as Record<string | number, unknown>)
    : { ...(root as object) };
  copy[key] = nextChild;
  return copy;
}

export function applyOp(root: unknown, op: Op): unknown {
  switch (op.type) {
    case 'set':
      return setIn(root, op.path, op.value);
    case 'unset':
      return unsetIn(root, op.path);
    case 'merge': {
      const current = getIn(root, op.path);
      const base = current != null && typeof current === 'object' ? current : {};
      return setIn(root, op.path, { ...(base as object), ...op.value });
    }
  }
}

export function applyOps(root: unknown, ops: Iterable<Op>): unknown {
  let state = root;
  for (const op of ops) state = applyOp(state, op);
  return state;
}

export function replayOps<S>(initialState: S, ops: Iterable<Op>): S {
  return applyOps(initialState, ops) as S;
}
