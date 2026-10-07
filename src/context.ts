export class ContextStore {
  #values = new Map<string, boolean>();
  #version = 0;

  get version(): number {
    return this.#version;
  }

  has(key: string): boolean {
    return this.#values.has(key);
  }

  get(key: string): boolean | undefined {
    return this.#values.get(key);
  }

  set(key: string, value: boolean): boolean {
    if (this.#values.get(key) === value) return false;
    this.#values.set(key, value);
    this.#version += 1;
    return true;
  }

  unset(key: string): boolean {
    if (!this.#values.delete(key)) return false;
    this.#version += 1;
    return true;
  }

  snapshot(): Record<string, boolean> {
    return Object.fromEntries([...this.#values.entries()].sort(([a], [b]) => (a < b ? -1 : 1)));
  }
}
