/**
 * 协议模型：通信有限状态机（CFSM）。
 *
 * 每个协议由一组状态和带标签的迁移组成，迁移标签分三类：
 *   - `!msg`  发送消息（send）
 *   - `?msg`  接收消息（recv）
 *   - `tau`/`τ` 内部动作（internal，不产生任何消息）
 */

export type ActionKind = "send" | "recv" | "internal";

export interface Action {
  readonly kind: ActionKind;
  /** send / recv 动作携带的消息名；internal 动作为 undefined */
  readonly message?: string;
}

export interface Transition {
  readonly from: string;
  readonly to: string;
  readonly action: Action;
}

export interface Protocol {
  readonly name: string;
  readonly states: readonly string[];
  readonly initial: string;
  /** 终止（接受）状态：双方都处于终止状态且队列清空才算一次正常终止 */
  readonly finals: readonly string[];
  readonly transitions: readonly Transition[];
}

/** 按源状态索引后的协议，便于快速查询某状态的出边 */
export interface IndexedProtocol {
  readonly protocol: Protocol;
  readonly outgoing: ReadonlyMap<string, readonly Transition[]>;
}

/** 协议描述的 JSON 格式 */
export interface ProtocolJson {
  name: string;
  initial: string;
  finals?: string[];
  transitions: Array<{ from: string; label: string; to: string }>;
}

/** 解析迁移标签：`!msg` 发送、`?msg` 接收、`tau`/`τ` 内部动作 */
export function parseLabel(label: string): Action {
  const trimmed = label.trim();
  if (trimmed.startsWith("!")) {
    const message = trimmed.slice(1).trim();
    if (!message) throw new Error(`发送动作缺少消息名: "${label}"`);
    return { kind: "send", message };
  }
  if (trimmed.startsWith("?")) {
    const message = trimmed.slice(1).trim();
    if (!message) throw new Error(`接收动作缺少消息名: "${label}"`);
    return { kind: "recv", message };
  }
  if (trimmed === "τ" || trimmed.toLowerCase() === "tau" || trimmed === "") {
    return { kind: "internal" };
  }
  throw new Error(`无法识别的动作标签: "${label}"（应为 !msg、?msg 或 tau）`);
}

export function parseProtocol(json: ProtocolJson): Protocol {
  if (!json.name) throw new Error("协议缺少 name 字段");
  if (!json.initial) throw new Error(`协议 ${json.name} 缺少 initial 字段`);
  const finals = json.finals ?? [];
  const transitions: Transition[] = json.transitions.map((t) => ({
    from: t.from,
    to: t.to,
    action: parseLabel(t.label),
  }));

  const stateSet = new Set<string>([json.initial, ...finals]);
  for (const t of transitions) {
    stateSet.add(t.from);
    stateSet.add(t.to);
  }
  for (const s of stateSet) {
    if (/[|,]/.test(s)) throw new Error(`状态名含有非法字符（| 或 ,）: "${s}"`);
  }
  for (const t of transitions) {
    if (t.action.message && /[|,]/.test(t.action.message)) {
      throw new Error(`消息名含有非法字符（| 或 ,）: "${t.action.message}"`);
    }
  }

  return {
    name: json.name,
    states: [...stateSet],
    initial: json.initial,
    finals,
    transitions,
  };
}

export function indexProtocol(protocol: Protocol): IndexedProtocol {
  const outgoing = new Map<string, Transition[]>();
  for (const t of protocol.transitions) {
    const list = outgoing.get(t.from) ?? [];
    list.push(t);
    outgoing.set(t.from, list);
  }
  return { protocol, outgoing };
}
