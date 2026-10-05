/** 判定结果与执行轨迹的可读化输出 */

import { Action, GlobalState, Party, Step, Verdict } from "./checker";

export interface PartyNames {
  readonly A: string;
  readonly B: string;
}

export function formatAction(a: Action): string {
  switch (a.kind) {
    case "send":
      return `!${a.message}`;
    case "recv":
      return `?${a.message}`;
    case "internal":
      return "τ";
  }
}

export function formatState(s: GlobalState, names: PartyNames): string {
  const q = (xs: readonly string[]) => (xs.length > 0 ? xs.join(",") : "空");
  return (
    `{${names.A}@${s.a}, ${names.B}@${s.b} | ` +
    `${names.A}→${names.B}:[${q(s.qAB)}] ${names.B}→${names.A}:[${q(s.qBA)}]}`
  );
}

export function formatTrace(trace: readonly Step[], initial: GlobalState, names: PartyNames): string {
  const lines = [`    初始  ${formatState(initial, names)}`];
  trace.forEach((s, i) => {
    const no = String(i + 1).padStart(2, " ");
    lines.push(`    ${no}.  [${names[s.party]}] ${formatAction(s.action)}  ⇒  ${formatState(s.to, names)}`);
  });
  return lines.join("\n");
}

function localStateOf(s: GlobalState, party: Party): string {
  return party === "A" ? s.a : s.b;
}

export function formatVerdict(v: Verdict, initial: GlobalState, names: PartyNames): string {
  switch (v.kind) {
    case "compatible":
      return [
        `✔ 兼容：新版可以与旧版对端配合完成全部规定会话。`,
        `  共探索 ${v.explored} 个全局状态，${v.terminalCount} 个正常终止点`,
        `  （双方均到达终止状态且两条队列均已清空）。`,
      ].join("\n");

    case "unexpected-reception": {
      const who = names[v.party];
      return [
        `✘ 不兼容：意外接收（unspecified reception）。`,
        `  ${who} 在状态 "${localStateOf(v.state, v.party)}" 收到队首消息 "${v.message}"，`,
        `  但该状态没有任何接收迁移能消费它（FIFO 队首阻塞，后续消息也无法被接收）。`,
        `  最短轨迹（${v.trace.length} 步）：`,
        formatTrace(v.trace, initial, names),
      ].join("\n");
    }

    case "deadlock":
      return [
        `✘ 不兼容：死锁 —— ${v.info.description}。`,
        `  这不是正常终止：正常终止要求双方都处于终止状态且队列清空。`,
        `  最短轨迹（${v.trace.length} 步）：`,
        formatTrace(v.trace, initial, names),
      ].join("\n");

    case "non-terminating": {
      const lines = [
        `✘ 不兼容：存在无法正常终止的会话（活锁 / 无限循环）。`,
        `  以下全局状态可达，但从它出发永远无法到达正常终止：`,
        `    ${formatState(v.state, names)}`,
        `  到达该状态的最短轨迹（${v.trace.length} 步）：`,
        formatTrace(v.trace, initial, names),
      ];
      if (v.cycle && v.cycle.length > 0) {
        lines.push(`  可无限重复的循环（${v.cycle.length} 步）：`);
        v.cycle.forEach((s, i) => {
          lines.push(
            `    ${String(i + 1).padStart(2, " ")}.  [${names[s.party]}] ${formatAction(s.action)}  ⇒  ${formatState(s.to, names)}`
          );
        });
      }
      return lines.join("\n");
    }
  }
}
