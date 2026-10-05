import type { SimResult, TickRecord } from "./simulator.ts";

/** 沿捐赠链找到有效优先级的最终来源任务（传递继承时 hops > 1） */
function inheritSource(rec: TickRecord, id: string): { source: string; transitive: boolean } | null {
  const donors = new Map(rec.inherited.map((i) => [i.id, i.from]));
  if (!donors.has(id)) return null;
  let cur = id;
  let hops = 0;
  const seen = new Set<string>();
  while (donors.has(cur) && !seen.has(cur)) {
    seen.add(cur);
    cur = donors.get(cur)!;
    hops += 1;
  }
  return { source: cur, transitive: hops > 1 };
}

function prioLabel(rec: TickRecord, id: string, eff: number, base: number): string {
  const src = inheritSource(rec, id);
  if (!src || eff === base) return `eff=${eff}`;
  const via = src.transitive ? " transitively" : "";
  return `eff=${eff}(base ${base}, inherited${via} from ${src.source})`;
}

export function formatTick(rec: TickRecord): string {
  const time =
    rec.duration > 1 ? `t=${rec.time}..${rec.time + rec.duration - 1}` : `t=${rec.time}`;
  const parts: string[] = [`[${time.padStart(9)}]`];

  if (rec.running) {
    const r = rec.running;
    parts.push(`RUN ${r.id} ${prioLabel(rec, r.id, r.eff, r.base)}`);
  } else if (rec.idle) {
    parts.push("IDLE (no runnable task)");
  } else {
    parts.push("STALLED (no runnable task)");
  }

  parts.push(
    rec.ready.length > 0
      ? `ready: ${rec.ready.map((t) => `${t.id}(${prioLabel(rec, t.id, t.eff, t.base)})`).join(", ")}`
      : "ready: -",
  );

  parts.push(
    rec.blocked.length > 0
      ? `blocked: ${rec.blocked
          .map((b) => {
            const where = b.holder ? `${b.lock} held by ${b.holder}` : `${b.lock} (free?)`;
            const tmo = b.timeoutIn === null ? "no timeout" : `timeout in ${b.timeoutIn}`;
            return `${b.id} waits ${where}, waited ${b.waited}, ${tmo}`;
          })
          .join("; ")}`
      : "blocked: -",
  );

  for (const cycle of rec.deadlockCycles) {
    parts.push(`DEADLOCK CYCLE: ${cycle.join(" -> ")}`);
  }

  if (rec.events.length > 0) {
    parts.push(`events: ${rec.events.join("; ")}`);
  }
  return parts.join(" | ");
}

export function formatResult(res: SimResult): string {
  const lines: string[] = [];
  lines.push(`=== scenario: ${res.name} ===`);
  for (const rec of res.ticks) {
    lines.push(formatTick(rec));
  }
  const end = res.ticks.length > 0 ? res.ticks[res.ticks.length - 1]!.time + 1 : 0;
  const statusLabel =
    res.status === "completed"
      ? "COMPLETED"
      : res.status === "deadlock"
        ? "DEADLOCK (permanent, not idle)"
        : "MAX TICKS EXCEEDED";
  lines.push(`=== status: ${statusLabel} (ends at t=${end}) ===`);
  for (const t of res.tasks) {
    const what = t.aborted
      ? `aborted at t=${t.completedAt}`
      : t.state === "done"
        ? `completed at t=${t.completedAt}`
        : `not finished (state=${t.state})`;
    lines.push(`  task ${t.id} (base pri ${t.basePriority}): ${what}`);
  }
  if (res.deadlocks.length > 0) {
    lines.push(`  lock-wait cycle(s) detected:`);
    for (const c of res.deadlocks) {
      lines.push(`    ${c.join(" -> ")}`);
    }
  }
  return lines.join("\n");
}
