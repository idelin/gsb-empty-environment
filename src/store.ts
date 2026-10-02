/** Minimal in-memory key/value store that the transactions run against. */
export class DataStore {
  private readonly data = new Map<string, number>();

  constructor(initial: Record<string, number> = {}) {
    for (const [key, value] of Object.entries(initial)) this.data.set(key, value);
  }

  get(key: string): number | undefined {
    return this.data.get(key);
  }

  set(key: string, value: number): void {
    this.data.set(key, value);
  }

  delete(key: string): void {
    this.data.delete(key);
  }

  entries(): Record<string, number> {
    return Object.fromEntries(this.data);
  }
}
