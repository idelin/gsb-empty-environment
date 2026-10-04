export type RegistrationStatus = 'waitlisted' | 'confirmed' | 'cancelled';

export interface Session {
  id: string;
  capacity: number;
  /** 同一互斥组内，一人至多持有一个确认名额 */
  mutexGroup: string;
}

export interface Registration {
  id: string;
  personId: string;
  sessionId: string;
  /** 排队优先级：数值大者排前 */
  priority: number;
  /** 报名序号：全局递增，小者排前；终身不变，撤销恢复时沿用 */
  seq: number;
  /** 个人偏好：1 表示最偏好，仅在同一互斥组内比较 */
  preference: number;
  status: RegistrationStatus;
  /** 撤销确认后置位：保留原候补位置，但不再被自动递补回该场次 */
  declinedOffer: boolean;
}

export type ChainEventKind =
  | 'registered'
  | 'promoted'
  | 'released'
  | 'revoked'
  | 'cancelled'
  | 'reinstated';

export interface ChainEvent {
  id: number;
  kind: ChainEventKind;
  personId: string;
  sessionId: string;
  /** promoted 时：被放弃的、较不偏好的原确认场次 */
  fromSessionId: string | null;
  /** promoted/released 时：触发本事件的上游事件 id；null 表示占用初始名额 */
  causedBy: number | null;
  detail: string;
}

export interface QueueEntry {
  position: number;
  personId: string;
  priority: number;
  seq: number;
  preference: number;
  declinedOffer: boolean;
}

const MAX_STABILIZE_STEPS = 100_000;

export class WaitlistEngine {
  private readonly sessions = new Map<string, Session>();
  private readonly registrations = new Map<string, Registration>();
  private readonly byPerson = new Map<string, Set<string>>();
  private readonly chain: ChainEvent[] = [];
  private readonly pendingCauses = new Map<string, number[]>();
  private seqCounter = 0;
  private eventCounter = 0;

  addSession(id: string, capacity: number, mutexGroup: string = id): void {
    if (this.sessions.has(id)) throw new Error(`场次已存在: ${id}`);
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new Error(`容量必须为正整数: ${id}`);
    }
    this.sessions.set(id, { id, capacity, mutexGroup });
  }

  register(
    personId: string,
    sessionId: string,
    options: { priority?: number; preference: number },
  ): Registration {
    this.mustSession(sessionId);
    const { priority = 0, preference } = options;
    if (!Number.isInteger(preference) || preference < 1) {
      throw new Error(`preference 必须是从 1 开始的整数: ${personId}`);
    }
    const dup = this.regsOf(personId).find(
      (r) => r.sessionId === sessionId && r.status !== 'cancelled',
    );
    if (dup) throw new Error(`重复报名: ${personId} @ ${sessionId}`);

    const seq = ++this.seqCounter;
    const reg: Registration = {
      id: `R${seq}`,
      personId,
      sessionId,
      priority,
      seq,
      preference,
      status: 'waitlisted',
      declinedOffer: false,
    };
    this.registrations.set(reg.id, reg);
    this.personRegs(personId).add(reg.id);
    this.record(
      'registered', personId, sessionId, null, null,
      `优先级 ${priority}，序号 ${seq}，偏好第 ${preference} 位`,
    );
    this.stabilize();
    return reg;
  }

  /** 取消报名：彻底退出该场次（确认中则释放名额）；日后再报将拿到新序号、重新排尾 */
  cancel(personId: string, sessionId: string): void {
    const reg = this.mustActive(personId, sessionId);
    const freesSpot = reg.status === 'confirmed';
    reg.status = 'cancelled';
    const ev = this.record(
      'cancelled', personId, sessionId, null, null,
      freesSpot ? '取消确认，释放名额' : '取消候补',
    );
    if (freesSpot) this.pushCause(sessionId, ev.id);
    this.stabilize();
  }

  /**
   * 撤销确认：按原 (priority, seq) 回到候补队列原位，绝不重新排尾；
   * 同时标记 declinedOffer，避免刚撤销又被同一空位立即递补回去。
   * 之后可通过 reinstate 恢复该场次的递补资格。
   */
  revokeConfirmation(personId: string, sessionId: string): void {
    const reg = this.mustActive(personId, sessionId);
    if (reg.status !== 'confirmed') {
      throw new Error(`仅已确认的报名可撤销: ${personId} @ ${sessionId}`);
    }
    reg.status = 'waitlisted';
    reg.declinedOffer = true;
    const ev = this.record(
      'revoked', personId, sessionId, null, null,
      '撤销确认，按原候补资格恢复',
    );
    this.pushCause(sessionId, ev.id);
    this.stabilize();
    const pos = this.queueOf(sessionId).findIndex((r) => r.id === reg.id) + 1;
    ev.detail += `（候补第 ${pos} 位，序号仍为 ${reg.seq}）`;
  }

  /** 恢复被撤销场次的递补资格（清除 declinedOffer 标记） */
  reinstate(personId: string, sessionId: string): void {
    const reg = this.mustActive(personId, sessionId);
    if (reg.status !== 'waitlisted' || !reg.declinedOffer) {
      throw new Error(`无需恢复递补资格: ${personId} @ ${sessionId}`);
    }
    reg.declinedOffer = false;
    this.record('reinstated', personId, sessionId, null, null, '恢复递补资格');
    this.stabilize();
  }

  getChain(): readonly ChainEvent[] {
    return this.chain;
  }

  eventsSince(eventId: number): ChainEvent[] {
    return this.chain.filter((e) => e.id > eventId);
  }

  lastEventId(): number {
    return this.eventCounter;
  }

  getQueue(sessionId: string): QueueEntry[] {
    this.mustSession(sessionId);
    return this.queueOf(sessionId).map((r, i) => ({
      position: i + 1,
      personId: r.personId,
      priority: r.priority,
      seq: r.seq,
      preference: r.preference,
      declinedOffer: r.declinedOffer,
    }));
  }

  getConfirmed(sessionId: string): string[] {
    this.mustSession(sessionId);
    return [...this.registrations.values()]
      .filter((r) => r.sessionId === sessionId && r.status === 'confirmed')
      .map((r) => r.personId);
  }

  confirmedOf(personId: string, mutexGroup: string): Registration | null {
    for (const id of this.personRegs(personId)) {
      const r = this.registrations.get(id)!;
      if (r.status === 'confirmed' && this.sessions.get(r.sessionId)!.mutexGroup === mutexGroup) {
        return r;
      }
    }
    return null;
  }

  formatEvent(e: ChainEvent): string {
    const cause = e.causedBy == null ? '' : `（因 #${e.causedBy}）`;
    switch (e.kind) {
      case 'registered':
        return `#${e.id} 报名 ${e.personId} → ${e.sessionId}：${e.detail}`;
      case 'promoted': {
        const from = e.fromSessionId ? `（放弃 ${e.fromSessionId}）` : '';
        return `#${e.id} 递补 ${e.personId} → ${e.sessionId}${from}${cause}`;
      }
      case 'released':
        return `#${e.id} 释放 ${e.personId} 让出 ${e.sessionId}${cause}：${e.detail}`;
      case 'revoked':
        return `#${e.id} 撤销 ${e.personId} @ ${e.sessionId}：${e.detail}`;
      case 'cancelled':
        return `#${e.id} 取消 ${e.personId} @ ${e.sessionId}：${e.detail}`;
      case 'reinstated':
        return `#${e.id} 恢复 ${e.personId} @ ${e.sessionId}：${e.detail}`;
    }
  }

  formatChain(events?: readonly ChainEvent[]): string {
    return (events ?? this.chain).map((e) => this.formatEvent(e)).join('\n');
  }

  /**
   * 递补直到稳定：每次操作后调用。
   * 收敛性：每次递补都使被递补者的确认场次偏好严格改进（或从无到有），
   * 每人偏好档位有限，故必然终止；另设步数上限兜底防循环。
   */
  private stabilize(): void {
    let steps = 0;
    for (;;) {
      let progressed = false;
      for (const session of this.sessions.values()) {
        while (this.freeSpots(session.id) > 0) {
          if (++steps > MAX_STABILIZE_STEPS) {
            throw new Error('递补未收敛：疑似循环');
          }
          const candidate = this.queueOf(session.id).find((r) => this.isEligible(r, session));
          if (!candidate) break;
          this.promote(candidate, session);
          progressed = true;
        }
      }
      if (!progressed) return;
    }
  }

  private promote(reg: Registration, session: Session): void {
    const causedBy = this.popCause(session.id);
    const current = this.confirmedOf(reg.personId, session.mutexGroup);
    const fromSessionId = current ? current.sessionId : null;
    reg.status = 'confirmed';
    const promo = this.record(
      'promoted', reg.personId, session.id, fromSessionId, causedBy,
      fromSessionId ? `${fromSessionId} → ${session.id}` : `获得 ${session.id} 名额`,
    );
    if (current) {
      // 放弃较不偏好的场次：回到该场原候补位置（priority/seq 不变），名额进入级联
      current.status = 'waitlisted';
      const rel = this.record(
        'released', reg.personId, current.sessionId, null, promo.id,
        `升入更偏好的 ${session.id}，${current.sessionId} 回到原候补位置`,
      );
      this.pushCause(current.sessionId, rel.id);
    }
  }

  /** 只接受比当前确认场次严格更偏好的场次 —— 偏好严格改进保证收敛、防止循环 */
  private isEligible(reg: Registration, session: Session): boolean {
    if (reg.status !== 'waitlisted' || reg.declinedOffer) return false;
    const current = this.confirmedOf(reg.personId, session.mutexGroup);
    if (!current) return true;
    return reg.preference < current.preference;
  }

  private queueOf(sessionId: string): Registration[] {
    return [...this.registrations.values()]
      .filter((r) => r.sessionId === sessionId && r.status === 'waitlisted')
      .sort((a, b) => b.priority - a.priority || a.seq - b.seq);
  }

  private freeSpots(sessionId: string): number {
    const s = this.sessions.get(sessionId)!;
    let used = 0;
    for (const r of this.registrations.values()) {
      if (r.sessionId === sessionId && r.status === 'confirmed') used++;
    }
    return s.capacity - used;
  }

  private record(
    kind: ChainEventKind,
    personId: string,
    sessionId: string,
    fromSessionId: string | null,
    causedBy: number | null,
    detail: string,
  ): ChainEvent {
    const ev: ChainEvent = {
      id: ++this.eventCounter, kind, personId, sessionId, fromSessionId, causedBy, detail,
    };
    this.chain.push(ev);
    return ev;
  }

  private pushCause(sessionId: string, eventId: number): void {
    const list = this.pendingCauses.get(sessionId) ?? [];
    list.push(eventId);
    this.pendingCauses.set(sessionId, list);
  }

  private popCause(sessionId: string): number | null {
    const list = this.pendingCauses.get(sessionId);
    return list && list.length > 0 ? list.shift()! : null;
  }

  private regsOf(personId: string): Registration[] {
    return [...this.personRegs(personId)].map((id) => this.registrations.get(id)!);
  }

  private personRegs(personId: string): Set<string> {
    let set = this.byPerson.get(personId);
    if (!set) {
      set = new Set();
      this.byPerson.set(personId, set);
    }
    return set;
  }

  private mustSession(sessionId: string): Session {
    const s = this.sessions.get(sessionId);
    if (!s) throw new Error(`场次不存在: ${sessionId}`);
    return s;
  }

  private mustActive(personId: string, sessionId: string): Registration {
    const reg = this.regsOf(personId).find(
      (r) => r.sessionId === sessionId && r.status !== 'cancelled',
    );
    if (!reg) throw new Error(`报名不存在或已取消: ${personId} @ ${sessionId}`);
    return reg;
  }
}
