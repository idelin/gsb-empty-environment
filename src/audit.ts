/** 审计动作类型。 */
export type AuditAction = 'store' | 'reveal' | 'autofill' | 'deny';

export interface AuditEntry {
  readonly seq: number;
  readonly time: string;
  readonly action: AuditAction;
  readonly credentialId: string | null;
  readonly allowed: boolean;
  readonly reason: string;
  readonly prevHash: string;
  readonly hash: string;
}

async function sha256Hex(input: string): Promise<string> {
  const data = new TextEncoder().encode(input);
  const digest = await crypto.subtle.digest('SHA-256', data);
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('');
}

/**
 * 仅追加、哈希链防篡改的审计日志。
 * 每条记录包含前一条的哈希，任何删改都会使 verify() 失败。
 */
export class AuditLog {
  #entries: AuditEntry[] = [];
  #lastHash = 'genesis';
  #seq = 0;

  async record(
    action: AuditAction,
    credentialId: string | null,
    allowed: boolean,
    reason: string,
  ): Promise<AuditEntry> {
    const base = {
      seq: this.#seq++,
      time: new Date().toISOString(),
      action,
      credentialId,
      allowed,
      reason,
      prevHash: this.#lastHash,
    };
    const hash = await sha256Hex(JSON.stringify(base));
    const entry: AuditEntry = Object.freeze({ ...base, hash });
    this.#entries.push(entry);
    this.#lastHash = hash;
    return entry;
  }

  entries(): readonly AuditEntry[] {
    return this.#entries;
  }

  /** 校验哈希链完整性。 */
  async verify(): Promise<boolean> {
    let prev = 'genesis';
    for (let i = 0; i < this.#entries.length; i++) {
      const e = this.#entries[i];
      if (e.seq !== i || e.prevHash !== prev) return false;
      const { hash, ...rest } = e;
      if ((await sha256Hex(JSON.stringify(rest))) !== hash) return false;
      prev = hash;
    }
    return true;
  }
}
