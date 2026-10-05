/**
 * 候补递补引擎
 *
 * 规则：
 * 1. 一人可报名多个互斥场次（preferences 按偏好从高到低排列），但同一时刻最多持有一个名额。
 * 2. 每个场次的候补队列按「优先级降序、报名序号升序」排序。
 * 3. 递补时只接受「比当前持有场次更偏好」的场次；由此保证每人偏好序严格前进，杜绝循环。
 * 4. 某人升入更偏好的场次后会释放原场次名额，可能触发级联递补；settle() 迭代直至稳定。
 * 5. 撤销确认时，按原报名序号与优先级恢复候补资格（插回原位），而非重新排尾；
 *    被撤销的场次记入 declined，不会再补给本人，防止立即回环。
 */

export interface PersonInput {
  id: string;
  /** 数值越大优先级越高 */
  priority: number;
  /** 互斥场次志愿，按偏好从高到低 */
  preferences: string[];
}

export type ChainEvent =
  | {
      kind: "promote";
      step: number;
      personId: string;
      /** null 表示此前没有名额 */
      fromSessionId: string | null;
      toSessionId: string;
      /** 因升级而释放名额的场次 */
      freedSessionId: string | null;
    }
  | {
      kind: "revoke";
      step: number;
      personId: string;
      sessionId: string;
    };

interface WaitlistEntry {
  personId: string;
  priority: number;
  /** 报名序号：一次报名分配一个全局递增序号，恢复候补时沿用原序号 */
  seq: number;
}

interface Registration {
  priority: number;
  seq: number;
  preferences: string[];
}

interface SessionState {
  id: string;
  capacity: number;
  occupants: Set<string>;
}

export class WaitlistEngine {
  private sessions = new Map<string, SessionState>();
  private waitlists = new Map<string, WaitlistEntry[]>();
  private registrations = new Map<string, Registration>();
  /** personId -> 当前持有场次 */
  private assignments = new Map<string, string>();
  /** personId -> 已拒绝（撤销过）的场次，不再补给本人 */
  private declined = new Map<string, Set<string>>();
  private seqCounter = 0;
  private chain: ChainEvent[] = [];

  addSession(id: string, capacity: number): void {
    if (this.sessions.has(id)) throw new Error(`场次 ${id} 已存在`);
    if (!Number.isInteger(capacity) || capacity < 0) {
      throw new Error(`场次 ${id} 容量非法: ${capacity}`);
    }
    this.sessions.set(id, { id, capacity, occupants: new Set() });
    this.waitlists.set(id, []);
  }

  /** 报名：为此人的每个志愿场次建立候补条目，共用同一个报名序号 */
  register(person: PersonInput): void {
    if (this.registrations.has(person.id)) {
      throw new Error(`人员 ${person.id} 已报名`);
    }
    if (person.preferences.length === 0) {
      throw new Error(`人员 ${person.id} 至少需要一个志愿场次`);
    }
    for (const sessionId of person.preferences) {
      if (!this.sessions.has(sessionId)) throw new Error(`未知场次 ${sessionId}`);
    }
    const seq = ++this.seqCounter;
    this.registrations.set(person.id, {
      priority: person.priority,
      seq,
      preferences: [...person.preferences],
    });
    for (const sessionId of person.preferences) {
      this.insertEntry(sessionId, {
        personId: person.id,
        priority: person.priority,
        seq,
      });
    }
  }

  /**
   * 执行递补直到稳定：没有空余名额、或空余名额没有可接受的候补者为止。
   * 返回本次产生的递补事件（已追加到完整递补链）。
   */
  settle(): ChainEvent[] {
    const events: ChainEvent[] = [];
    let progress = true;
    while (progress) {
      progress = false;
      for (const session of this.sessions.values()) {
        while (session.occupants.size < session.capacity) {
          const entry = this.findNextEligible(session.id);
          if (!entry) break;
          this.removeEntry(session.id, entry.personId);

          const from = this.assignments.get(entry.personId) ?? null;
          if (from !== null) {
            this.sessions.get(from)!.occupants.delete(entry.personId);
          }
          session.occupants.add(entry.personId);
          this.assignments.set(entry.personId, session.id);

          const event: ChainEvent = {
            kind: "promote",
            step: this.chain.length + 1,
            personId: entry.personId,
            fromSessionId: from,
            toSessionId: session.id,
            freedSessionId: from,
          };
          this.chain.push(event);
          events.push(event);
          progress = true;
        }
      }
    }
    return events;
  }

  /**
   * 撤销确认：释放当前名额，按原报名序号恢复其他志愿场次的候补资格，
   * 然后重新递补至稳定。返回撤销事件及其引发的级联递补事件。
   */
  revoke(personId: string): ChainEvent[] {
    const current = this.assignments.get(personId);
    if (current === undefined) {
      throw new Error(`人员 ${personId} 当前没有已确认的场次`);
    }
    const reg = this.registrations.get(personId)!;

    this.sessions.get(current)!.occupants.delete(personId);
    this.assignments.delete(personId);

    // 拒绝该场次，避免紧接着又被补回同一场次造成循环
    let declinedSet = this.declined.get(personId);
    if (!declinedSet) {
      declinedSet = new Set();
      this.declined.set(personId, declinedSet);
    }
    declinedSet.add(current);

    const revokeEvent: ChainEvent = {
      kind: "revoke",
      step: this.chain.length + 1,
      personId,
      sessionId: current,
    };
    this.chain.push(revokeEvent);

    // 按原优先级与原报名序号恢复候补（insertEntry 去重并保持有序），而非重新排尾
    for (const sessionId of reg.preferences) {
      if (declinedSet.has(sessionId)) continue;
      this.insertEntry(sessionId, {
        personId,
        priority: reg.priority,
        seq: reg.seq,
      });
    }

    return [revokeEvent, ...this.settle()];
  }

  /** 完整递补链（含撤销事件），step 全局递增 */
  getChain(): readonly ChainEvent[] {
    return this.chain;
  }

  /** 可读的递补链文本 */
  formatChain(events: readonly ChainEvent[] = this.chain): string[] {
    return events.map((e) => {
      if (e.kind === "revoke") {
        return `#${e.step} 撤销 ${e.personId}: 放弃 ${e.sessionId}，按原报名序号恢复候补资格`;
      }
      const from = e.fromSessionId ?? "无名额";
      const freed = e.freedSessionId ? `（释放 ${e.freedSessionId} 名额）` : "（首次获得名额）";
      return `#${e.step} 递补 ${e.personId}: ${from} -> ${e.toSessionId} ${freed}`;
    });
  }

  assignmentOf(personId: string): string | undefined {
    return this.assignments.get(personId);
  }

  /** 某场次当前候补队列中的人员顺序（含暂不满足接受条件者） */
  waitlistOf(sessionId: string): string[] {
    return (this.waitlists.get(sessionId) ?? []).map((e) => e.personId);
  }

  occupantsOf(sessionId: string): string[] {
    return [...(this.sessions.get(sessionId)?.occupants ?? [])];
  }

  /** 队列有序插入：优先级降序，报名序号升序；同一人不重复入队 */
  private insertEntry(sessionId: string, entry: WaitlistEntry): void {
    const queue = this.waitlists.get(sessionId)!;
    if (queue.some((e) => e.personId === entry.personId)) return;
    let lo = 0;
    let hi = queue.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (this.compareEntry(queue[mid], entry) < 0) lo = mid + 1;
      else hi = mid;
    }
    queue.splice(lo, 0, entry);
  }

  private compareEntry(a: WaitlistEntry, b: WaitlistEntry): number {
    if (a.priority !== b.priority) return b.priority - a.priority;
    return a.seq - b.seq;
  }

  private removeEntry(sessionId: string, personId: string): void {
    const queue = this.waitlists.get(sessionId)!;
    const index = queue.findIndex((e) => e.personId === personId);
    if (index >= 0) queue.splice(index, 1);
  }

  /**
   * 找到队列中第一个可接受该场次的候补者：
   * - 未拒绝过该场次；
   * - 当前无名额，或该场次比当前持有场次「严格更偏好」。
   * 不满足条件者保留在队列中（不影响他人），直接跳过。
   */
  private findNextEligible(sessionId: string): WaitlistEntry | null {
    const queue = this.waitlists.get(sessionId)!;
    for (const entry of queue) {
      const personId = entry.personId;
      if (this.declined.get(personId)?.has(sessionId)) continue;
      const current = this.assignments.get(personId);
      if (current === undefined) return entry;
      if (current === sessionId) continue;
      const preferences = this.registrations.get(personId)!.preferences;
      const currentIndex = preferences.indexOf(current);
      const offerIndex = preferences.indexOf(sessionId);
      if (offerIndex >= 0 && offerIndex < currentIndex) return entry;
    }
    return null;
  }
}
