export type HeaderPrimitive = string | number | boolean | null | undefined;
export type HeaderValue = HeaderPrimitive | readonly HeaderPrimitive[];

/**
 * Structural subset of the Fetch API `Headers` object. This keeps the package
 * usable from Node, workerd, Deno and browser runtimes without depending on a
 * particular `Headers` implementation.
 */
export interface HeaderReader {
  get(name: string): string | null | undefined;
  has?(name: string): boolean;
  forEach?(
    callback: (value: string, key: string, parent: unknown) => void,
  ): void;
}

export type HeaderSource =
  | HeaderReader
  | Readonly<Record<string, HeaderValue>>
  | Iterable<readonly [string, HeaderValue]>;

function isHeaderReader(value: unknown): value is HeaderReader {
  if (typeof value !== "object" || value === null) return false;
  return typeof (value as HeaderReader).get === "function";
}

function isIterable(
  value: unknown,
): value is Iterable<readonly [string, HeaderValue]> {
  if (typeof value !== "object" || value === null) return false;
  const iterator = (value as { [Symbol.iterator]?: unknown })[
    Symbol.iterator
  ];
  return typeof iterator === "function";
}

/**
 * Case-insensitive header collection. Presence is tracked separately from the
 * value so an explicitly empty `Origin`, `Referer`, `Cookie` or
 * `Sec-Fetch-Dest` header is still treated as present and can be rejected.
 */
export class HeaderBag {
  #values = new Map<string, string[]>();
  #reader: HeaderReader | null = null;

  constructor(source?: HeaderSource | null) {
    if (source === undefined || source === null) return;

    if (isHeaderReader(source)) {
      this.#reader = source;
      const forEach = source.forEach;
      if (forEach) {
        forEach.call(source, (value, name) => {
          this.#append(name, value);
        });
      }
      return;
    }

    if (isIterable(source)) {
      for (const pair of source) {
        if (!pair || pair.length < 2) continue;
        const name = pair[0];
        if (typeof name === "string") this.#append(name, pair[1]);
      }
      return;
    }

    for (const [name, value] of Object.entries(
      source as Readonly<Record<string, HeaderValue>>,
    )) {
      this.#append(name, value);
    }
  }

  #append(name: string, value: HeaderValue): void {
    if (value === undefined || value === null) return;
    const key = name.toLowerCase();
    const values = Array.isArray(value) ? value : [value];
    const existing = this.#values.get(key);

    if (existing) {
      for (const item of values) existing.push(String(item));
      return;
    }

    this.#values.set(
      key,
      values.map((item) => String(item)),
    );
  }

  has(name: string): boolean {
    if (this.#values.has(name.toLowerCase())) return true;
    const reader = this.#reader;
    if (reader) {
      if (typeof reader.has === "function") {
        return reader.has(name);
      }
      const value = reader.get(name);
      return value !== null && value !== undefined;
    }
    return false;
  }

  get(name: string): string | null {
    const values = this.#values.get(name.toLowerCase());
    if (!values || values.length === 0) {
      const reader = this.#reader;
      if (reader) {
        const value = reader.get(name);
        return value === undefined ? null : value;
      }
      return null;
    }
    return values.join(", ");
  }

  getFirst(name: string): string | null {
    const values = this.#values.get(name.toLowerCase());
    if (!values || values.length === 0) {
      const reader = this.#reader;
      if (reader) {
        const value = reader.get(name);
        return value === undefined ? null : value;
      }
      return null;
    }
    return values[0];
  }

  getAll(name: string): readonly string[] {
    const values = this.#values.get(name.toLowerCase());
    if (values && values.length > 0) return [...values];
    const reader = this.#reader;
    if (reader) {
      const value = reader.get(name);
      return value === null || value === undefined ? [] : [value];
    }
    return [];
  }

  toObject(): Record<string, string> {
    const result: Record<string, string> = {};
    for (const [name, values] of this.#values) {
      result[name] = values.join(", ");
    }
    return result;
  }
}
