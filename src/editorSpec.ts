/**
 * 示例规约：迷你行编辑器。
 * 层级状态：root(active) -> { idle, editing -> { clean, dirty } } + 顶层 locked。
 * 守卫：锁定时拒绝编辑；原子多字段动作：一次迁移同时改 text/lines/saved。
 */

import type { Config, Spec } from "./spec.ts";

export const MAX_LINES = 3;

export function editorSpecV1(): Spec {
  return {
    name: "editor",
    version: 1,
    states: [
      { id: "root", initial: "active" },
      { id: "active", parent: "root", initial: "idle" },
      { id: "idle", parent: "active" },
      { id: "editing", parent: "active", initial: "clean" },
      { id: "clean", parent: "editing" },
      { id: "dirty", parent: "editing" },
      { id: "locked", parent: "root" },
    ],
    initialData: () => ({ text: "", lines: 0, saved: true, lockCount: 0 }),
    transitions: [
      { event: "focus", from: "idle", to: "editing" },
      { event: "blur", from: "editing", to: "idle" },
      {
        event: "type",
        from: "editing",
        guard: (d, p) => typeof p.ch === "string" && (d.lines as number) < MAX_LINES,
        action: (d, p) => ({
          text: `${d.text}${p.ch}`,
          lines: (d.lines as number) + 1,
          saved: false,
        }),
        to: "dirty",
      },
      {
        event: "save",
        from: "dirty",
        action: () => ({ saved: true }),
        to: "clean",
        effects: ["persist"],
      },
      {
        event: "lock",
        from: "active",
        action: (d) => ({ lockCount: (d.lockCount as number) + 1 }),
        to: "locked",
      },
      { event: "unlock", from: "locked", to: "active" },
      { event: "reset", action: () => ({ text: "", lines: 0, saved: true }), to: "idle" },
    ],
    invariants: [
      { name: "lines-bounded", check: (d) => (d.lines as number) >= 0 && (d.lines as number) <= MAX_LINES },
      { name: "text-matches-lines", check: (d) => (d.text as string).length === (d.lines as number) },
      { name: "dirty-implies-unsaved", check: (d, path) => !path.includes("dirty") || d.saved === false },
    ],
    domains: {
      lines: [0, 1, 2, 3],
      saved: [true, false],
      lockCount: [0, 1, 2],
    },
    eventDomains: {
      focus: [{}],
      blur: [{}],
      type: [{ ch: "a" }, { ch: "b" }],
      save: [{}],
      lock: [{}],
      unlock: [{}],
      reset: [{}],
    },
  };
}

/** v2：新增只读状态只读字段 readonly，并把 MAX_LINES 放宽到 4。 */
export function editorSpecV2(): Spec {
  const v1 = editorSpecV1();
  return {
    ...v1,
    version: 2,
    initialData: () => ({ ...v1.initialData(), readonly: false }),
    invariants: [
      ...v1.invariants,
      { name: "readonly-flag", check: (d) => d.readonly === false || d.readonly === true },
    ],
  };
}

/** v1 -> v2 迁移证据：补上 readonly 字段，路径保持不变。 */
export function migrateV1toV2(config: Config): Config {
  return { path: [...config.path], data: { ...config.data, readonly: false } };
}

/** 一个必然失败的迁移：伪造不存在的状态路径。 */
export function badMigration(config: Config): Config {
  return { path: ["root", "nonexistent"], data: { ...config.data } };
}
