export type Tag = string;

export class Tagged<V = unknown> {
  readonly value: V;
  readonly tags: ReadonlySet<Tag>;

  constructor(value: V, tags: Iterable<Tag> = []) {
    this.value = value;
    this.tags = new Set(tags);
  }
}

export type Unwrap<T> = T extends Tagged<infer V> ? V : T;

export function isTagged(value: unknown): value is Tagged<unknown> {
  return value instanceof Tagged;
}

const NO_TAGS: ReadonlySet<Tag> = new Set();

export function tagsOf(value: unknown): ReadonlySet<Tag> {
  return isTagged(value) ? value.tags : NO_TAGS;
}

export function rawOf<T>(value: T): Unwrap<T> {
  return (isTagged(value) ? value.value : value) as Unwrap<T>;
}

export function tag<V>(value: V, ...tags: Tag[]): Tagged<Unwrap<V>> {
  const merged = new Set(tagsOf(value));
  for (const t of tags) merged.add(t);
  return new Tagged(rawOf(value), merged);
}

type TagTransform = (tags: ReadonlySet<Tag>) => ReadonlySet<Tag>;

function isPlainObject(value: object): boolean {
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function deepMap(value: unknown, seen: Map<unknown, unknown>, fn: TagTransform): unknown {
  if (isTagged(value)) {
    if (seen.has(value)) return seen.get(value);
    const copy = new Tagged<unknown>(undefined, fn(value.tags));
    seen.set(value, copy);
    (copy as { value: unknown }).value = deepMap(value.value, seen, fn);
    return copy;
  }
  if (Array.isArray(value)) {
    if (seen.has(value)) return seen.get(value);
    const copy: unknown[] = [];
    seen.set(value, copy);
    for (const item of value) copy.push(deepMap(item, seen, fn));
    return copy;
  }
  if (value !== null && typeof value === "object" && isPlainObject(value)) {
    if (seen.has(value)) return seen.get(value);
    const copy: Record<string, unknown> = {};
    seen.set(value, copy);
    for (const key of Object.keys(value)) {
      copy[key] = deepMap((value as Record<string, unknown>)[key], seen, fn);
    }
    return copy;
  }
  return value;
}

export function clone<V>(value: V): V {
  return deepMap(value, new Map(), (tags) => tags) as V;
}

export function mergeArrays(...arrays: unknown[]): Tagged<unknown[]> {
  const out: unknown[] = [];
  const tags = new Set<Tag>();
  for (const entry of arrays) {
    for (const t of tagsOf(entry)) tags.add(t);
    const raw = rawOf(entry);
    if (!Array.isArray(raw)) {
      throw new TypeError("mergeArrays expects array arguments");
    }
    out.push(...raw);
  }
  return new Tagged(out, tags);
}

export function project<T, K extends keyof Unwrap<T>>(
  value: T,
  keys: readonly K[],
): Tagged<Pick<Unwrap<T>, K>> {
  const raw = rawOf(value);
  if (raw === null || typeof raw !== "object") {
    throw new TypeError("project expects an object");
  }
  const out: Record<PropertyKey, unknown> = {};
  for (const key of keys) {
    out[key] = (raw as Record<PropertyKey, unknown>)[key];
  }
  return new Tagged(out as Pick<Unwrap<T>, K>, tagsOf(value));
}

export function deriveString(...inputs: unknown[]): Tagged<string> {
  const tags = new Set<Tag>();
  let text = "";
  for (const input of inputs) {
    for (const t of tagsOf(input)) tags.add(t);
    text += String(rawOf(input));
  }
  return new Tagged(text, tags);
}

export type Untagger = <V>(value: V, ...tags: Tag[]) => V;

export function createUntagger(whitelist: Iterable<Tag>): Untagger {
  const allowed = new Set(whitelist);
  return <V>(value: V, ...tags: Tag[]): V => {
    for (const t of tags) {
      if (!allowed.has(t)) {
        throw new Error(`tag "${t}" is not in the untag whitelist`);
      }
    }
    const remove = new Set(tags);
    return deepMap(value, new Map(), (current) => {
      const next = new Set(current);
      for (const t of remove) next.delete(t);
      return next;
    }) as V;
  };
}
