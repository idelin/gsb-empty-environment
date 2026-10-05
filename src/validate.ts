import type { Scenario, Segment, TaskSpec } from "./types.ts";

function fail(msg: string): never {
  throw new Error(`invalid scenario: ${msg}`);
}

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function checkSegment(raw: unknown, where: string): Segment {
  if (!isObj(raw)) fail(`${where}: segment must be an object`);
  const type = raw.type;
  if (type === "compute") {
    const d = raw.duration;
    if (typeof d !== "number" || !Number.isInteger(d) || d < 0)
      fail(`${where}: compute.duration must be a non-negative integer`);
    return { type: "compute", duration: d };
  }
  if (type === "acquire") {
    if (typeof raw.lock !== "string" || raw.lock.length === 0)
      fail(`${where}: acquire.lock must be a non-empty string`);
    const seg: Segment = { type: "acquire", lock: raw.lock };
    if (raw.timeout !== undefined) {
      if (typeof raw.timeout !== "number" || !Number.isInteger(raw.timeout) || raw.timeout <= 0)
        fail(`${where}: acquire.timeout must be a positive integer`);
      seg.timeout = raw.timeout;
    }
    if (raw.onTimeout !== undefined) {
      if (raw.onTimeout !== "abort" && raw.onTimeout !== "continue")
        fail(`${where}: acquire.onTimeout must be "abort" or "continue"`);
      seg.onTimeout = raw.onTimeout;
    }
    return seg;
  }
  if (type === "release") {
    if (typeof raw.lock !== "string" || raw.lock.length === 0)
      fail(`${where}: release.lock must be a non-empty string`);
    return { type: "release", lock: raw.lock };
  }
  fail(`${where}: unknown segment type ${JSON.stringify(type)}`);
}

export function validateScenario(raw: unknown): Scenario {
  if (!isObj(raw)) fail("top level must be an object");
  if (!Array.isArray(raw.tasks) || raw.tasks.length === 0)
    fail("tasks must be a non-empty array");
  if (raw.maxTicks !== undefined) {
    if (typeof raw.maxTicks !== "number" || !Number.isInteger(raw.maxTicks) || raw.maxTicks <= 0)
      fail("maxTicks must be a positive integer");
  }
  const ids = new Set<string>();
  const tasks: TaskSpec[] = raw.tasks.map((rt, i) => {
    const where = `tasks[${i}]`;
    if (!isObj(rt)) fail(`${where}: must be an object`);
    if (typeof rt.id !== "string" || rt.id.length === 0)
      fail(`${where}: id must be a non-empty string`);
    if (ids.has(rt.id)) fail(`${where}: duplicate task id "${rt.id}"`);
    ids.add(rt.id);
    if (typeof rt.priority !== "number" || !Number.isFinite(rt.priority))
      fail(`${where} (${rt.id}): priority must be a finite number`);
    if (rt.releaseTime !== undefined) {
      if (typeof rt.releaseTime !== "number" || !Number.isInteger(rt.releaseTime) || rt.releaseTime < 0)
        fail(`${where} (${rt.id}): releaseTime must be a non-negative integer`);
    }
    if (!Array.isArray(rt.segments) || rt.segments.length === 0)
      fail(`${where} (${rt.id}): segments must be a non-empty array`);
    const segments = rt.segments.map((s, j) => checkSegment(s, `${where}.segments[${j}]`));
    return {
      id: rt.id,
      priority: rt.priority,
      ...(rt.releaseTime !== undefined ? { releaseTime: rt.releaseTime } : {}),
      segments,
    };
  });
  return {
    ...(typeof raw.name === "string" ? { name: raw.name } : {}),
    ...(raw.maxTicks !== undefined ? { maxTicks: raw.maxTicks as number } : {}),
    tasks,
  };
}
