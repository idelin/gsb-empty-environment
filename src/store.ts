/** 异步属性源：返回整数，或 undefined 表示属性缺失。 */
export type Fetcher = (subject: string, attr: string) => Promise<number | undefined>;

type Entry = { value: number | undefined; version: number };

/**
 * 版本化属性存储。每个 (subject, attr) 独立版本号；
 * update 热更新只递增对应键的版本，引擎据此精确失效缓存。
 */
export class AttrStore {
  private entries = new Map<string, Entry>();
  private fetcher: Fetcher;

  constructor(fetcher: Fetcher) {
    this.fetcher = fetcher;
  }

  private key(subject: string, attr: string): string {
    return `${subject} ${attr}`;
  }

  async read(subject: string, attr: string): Promise<Entry> {
    const key = this.key(subject, attr);
    let entry = this.entries.get(key);
    if (!entry) {
      const fetched = await this.fetcher(subject, attr);
      entry = { value: fetched === null ? undefined : fetched, version: 0 };
      this.entries.set(key, entry);
    }
    return { value: entry.value, version: entry.version };
  }

  /** 热更新某个主体的某个属性；只影响读过它的缓存条目。 */
  update(subject: string, attr: string, value: number | undefined): void {
    const key = this.key(subject, attr);
    const current = this.entries.get(key);
    this.entries.set(key, { value, version: (current?.version ?? -1) + 1 });
  }

  versionOf(subject: string, attr: string): number {
    return this.entries.get(this.key(subject, attr))?.version ?? -1;
  }
}
