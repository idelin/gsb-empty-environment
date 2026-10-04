import type { FileState, Precondition } from "./types.ts";

export interface PreconditionVerdict {
  ok: boolean;
  detail: string;
}

function toRegex(pattern: string, isRegex?: boolean): RegExp {
  return isRegex ? new RegExp(pattern) : new RegExp(escapeRegExp(pattern));
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** 前置条件读取哪个文件的内容（用于读写干涉分析） */
export function preconditionFile(pc: Precondition): string {
  return pc.file;
}

export function describePrecondition(pc: Precondition): string {
  switch (pc.kind) {
    case "fileExists":
      return `文件 ${pc.file} 必须存在`;
    case "contains":
      return `文件 ${pc.file} 必须包含 ${JSON.stringify(pc.pattern)}`;
    case "notContains":
      return `文件 ${pc.file} 不得包含 ${JSON.stringify(pc.pattern)}`;
    case "matches": {
      const bounds: string[] = [];
      if (pc.minCount !== undefined) bounds.push(`至少 ${pc.minCount} 处`);
      if (pc.maxCount !== undefined) bounds.push(`至多 ${pc.maxCount} 处`);
      return `文件 ${pc.file} 中 /${pc.regex}/ 的匹配数须满足：${
        bounds.join(" 且 ") || "任意"
      }`;
    }
  }
}

export function evaluatePrecondition(
  pc: Precondition,
  files: FileState,
): PreconditionVerdict {
  const content = files[pc.file];
  const desc = describePrecondition(pc);
  if (pc.kind === "fileExists") {
    return content !== undefined
      ? { ok: true, detail: desc }
      : { ok: false, detail: `${desc}，但文件不存在` };
  }
  if (content === undefined) {
    return { ok: false, detail: `${desc}，但文件 ${pc.file} 不存在` };
  }
  switch (pc.kind) {
    case "contains": {
      const re = toRegex(pc.pattern, pc.isRegex);
      return re.test(content)
        ? { ok: true, detail: desc }
        : { ok: false, detail: `${desc}，当前内容不满足` };
    }
    case "notContains": {
      const re = toRegex(pc.pattern, pc.isRegex);
      return re.test(content)
        ? { ok: false, detail: `${desc}，当前内容中仍存在匹配` }
        : { ok: true, detail: desc };
    }
    case "matches": {
      const re = new RegExp(pc.regex, "g");
      const count = [...content.matchAll(re)].length;
      if (pc.minCount !== undefined && count < pc.minCount) {
        return {
          ok: false,
          detail: `${desc}，实际匹配 ${count} 处`,
        };
      }
      if (pc.maxCount !== undefined && count > pc.maxCount) {
        return {
          ok: false,
          detail: `${desc}，实际匹配 ${count} 处`,
        };
      }
      return { ok: true, detail: desc };
    }
  }
}
