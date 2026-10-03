/**
 * Minimal in-memory key/value store representing the database pages/rows
 * that transactions read and write under lock protection.
 */
export class DataStore {
  private readonly data = new Map<string, number>();

  has(key: string): boolean {
    return this.data.has(key);
  }

  read(key: string): number | undefined {
    return this.data.get(key);
  }

  write(key: string, value: number): void {
    this.data.set(key, value);
  }

  delete(key: string): void {
    this.data.delete(key);
  }

  snapshot(): Record<string, number> {
    return Object.fromEntries([...this.data.entries()].sort(([a], [b]) => a.localeCompare(b)));
  }
}
