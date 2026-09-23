/**
 * 独立所有权账本：不依赖作用域内部状态，直接以物理资源实例
 * （对象按引用、原始值按值）为键，记录每次取得/释放。
 * audit() 校验每条资源“最终恰好释放一次、且无泄漏”。
 */

export interface LedgerEntry {
  label: string;
  acquires: number;
  releases: number;
}

export interface LedgerViolation {
  label: string;
  acquires: number;
  releases: number;
  problem: "leaked" | "released-without-acquire" | "double-release";
}

export class OwnershipLedger {
  #entries = new Map<unknown, LedgerEntry>();

  #entryFor(resource: unknown, label: string): LedgerEntry {
    let entry = this.#entries.get(resource);
    if (!entry) {
      entry = { label, acquires: 0, releases: 0 };
      this.#entries.set(resource, entry);
    }
    return entry;
  }

  acquire(resource: unknown, label: string): void {
    this.#entryFor(resource, label).acquires++;
  }

  release(resource: unknown, label: string): void {
    this.#entryFor(resource, label).releases++;
  }

  releasesOf(resource: unknown): number {
    return this.#entries.get(resource)?.releases ?? 0;
  }

  acquiresOf(resource: unknown): number {
    return this.#entries.get(resource)?.acquires ?? 0;
  }

  get size(): number {
    return this.#entries.size;
  }

  violations(): LedgerViolation[] {
    const out: LedgerViolation[] = [];
    for (const entry of this.#entries.values()) {
      if (entry.releases === 0 && entry.acquires > 0) {
        out.push({ ...entry, problem: "leaked" });
      } else if (entry.releases > entry.acquires) {
        out.push({
          ...entry,
          problem: entry.acquires === 0 ? "released-without-acquire" : "double-release",
        });
      }
    }
    return out;
  }

  /** 每条资源最终恰好释放一次（acquires === releases === 1）。 */
  audit(): void {
    const bad = this.violations();
    if (bad.length > 0) {
      const detail = bad
        .map((v) => `${v.label}: acquires=${v.acquires} releases=${v.releases} (${v.problem})`)
        .join("; ");
      throw new Error(`OwnershipLedger audit failed: ${detail}`);
    }
  }
}
