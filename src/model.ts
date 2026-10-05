/** 协议模型：单角色有限状态机，动作分为发送(!)、接收(?)、内部动作(τ)。 */

export type Side = 'A' | 'B';

export interface RawTransition {
  from: string;
  to: string;
  send?: string;
  recv?: string;
  tau?: boolean;
}

export interface ProtocolSpec {
  name: string;
  initial: string;
  finals: string[];
  transitions: RawTransition[];
}

export type ActionKind = 'send' | 'recv' | 'tau';

export interface Transition {
  from: string;
  to: string;
  kind: ActionKind;
  /** tau 动作时为空字符串 */
  msg: string;
}

export interface Machine {
  name: string;
  initial: string;
  finals: Set<string>;
  states: Set<string>;
  byState: Map<string, Transition[]>;
}

/** 把 JSON 规格编译成内部自动机，并做静态校验。 */
export function compile(spec: ProtocolSpec): Machine {
  if (!spec || typeof spec !== 'object') throw new Error('协议描述必须是 JSON 对象');
  if (typeof spec.name !== 'string' || !spec.name) throw new Error('缺少 name 字段');
  if (typeof spec.initial !== 'string' || !spec.initial) throw new Error(`协议 ${spec.name}: 缺少 initial 字段`);
  if (!Array.isArray(spec.finals)) throw new Error(`协议 ${spec.name}: finals 必须是数组`);
  if (!Array.isArray(spec.transitions)) throw new Error(`协议 ${spec.name}: transitions 必须是数组`);

  const states = new Set<string>([spec.initial, ...spec.finals]);
  const byState = new Map<string, Transition[]>();

  spec.transitions.forEach((t, i) => {
    const where = `协议 ${spec.name} 第 ${i + 1} 条迁移`;
    if (typeof t.from !== 'string' || typeof t.to !== 'string') {
      throw new Error(`${where}: from/to 必须是字符串`);
    }
    const kinds = [t.send !== undefined, t.recv !== undefined, t.tau === true].filter(Boolean).length;
    if (kinds !== 1) throw new Error(`${where}: 必须且只能指定 send / recv / tau 之一`);

    let tr: Transition;
    if (t.send !== undefined) tr = { from: t.from, to: t.to, kind: 'send', msg: mustStr(t.send, where, 'send') };
    else if (t.recv !== undefined) tr = { from: t.from, to: t.to, kind: 'recv', msg: mustStr(t.recv, where, 'recv') };
    else tr = { from: t.from, to: t.to, kind: 'tau', msg: '' };

    states.add(t.from);
    states.add(t.to);
    if (!byState.has(t.from)) byState.set(t.from, []);
    byState.get(t.from)!.push(tr);
  });

  if (!states.has(spec.initial)) throw new Error(`协议 ${spec.name}: initial 状态不存在`);
  for (const f of spec.finals) {
    if (typeof f !== 'string') throw new Error(`协议 ${spec.name}: finals 必须都是字符串`);
  }

  return { name: spec.name, initial: spec.initial, finals: new Set(spec.finals), states, byState };
}

function mustStr(v: unknown, where: string, field: string): string {
  if (typeof v !== 'string' || v === '') throw new Error(`${where}: ${field} 必须是非空字符串`);
  return v;
}

export function transitionsFrom(m: Machine, state: string): Transition[] {
  return m.byState.get(state) ?? [];
}

/** τ 闭包：只走内部动作能到达的状态集合（含自身）。 */
export function tauClosure(m: Machine, state: string): Set<string> {
  const seen = new Set<string>([state]);
  const stack = [state];
  while (stack.length > 0) {
    const s = stack.pop()!;
    for (const t of transitionsFrom(m, s)) {
      if (t.kind === 'tau' && !seen.has(t.to)) {
        seen.add(t.to);
        stack.push(t.to);
      }
    }
  }
  return seen;
}

/** 在 τ 闭包内可接收的消息集合。 */
export function receivableMessages(m: Machine, state: string): Set<string> {
  const out = new Set<string>();
  for (const s of tauClosure(m, state)) {
    for (const t of transitionsFrom(m, s)) {
      if (t.kind === 'recv') out.add(t.msg);
    }
  }
  return out;
}
