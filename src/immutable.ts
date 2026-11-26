/**
 * A small read-only map facade. `Object.freeze` does not freeze Map contents,
 * so exposing a raw Map would let callers mutate normalized description data.
 */
export class FrozenMap<K, V> {
  readonly #map: Map<K, V>;

  constructor(entries: Iterable<readonly [K, V]>) {
    this.#map = new Map(entries);
    Object.freeze(this);
  }

  get size(): number {
    return this.#map.size;
  }

  get(key: K): V | undefined {
    return this.#map.get(key);
  }

  has(key: K): boolean {
    return this.#map.has(key);
  }

  keys(): IterableIterator<K> {
    return this.#map.keys();
  }

  values(): IterableIterator<V> {
    return this.#map.values();
  }

  entries(): IterableIterator<[K, V]> {
    return this.#map.entries();
  }

  forEach(
    callbackfn: (value: V, key: K, map: ReadonlyMap<K, V>) => void,
    thisArg?: unknown,
  ): void {
    this.#map.forEach((value, key) => {
      callbackfn.call(thisArg, value, key, this as unknown as ReadonlyMap<K, V>);
    });
  }

  [Symbol.iterator](): IterableIterator<[K, V]> {
    return this.#map[Symbol.iterator]();
  }
}
