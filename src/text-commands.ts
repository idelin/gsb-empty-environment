import type { Command } from "./store.ts";

export interface TextBuffer {
  text: string;
  edits: number;
}

/** 在 pos 处插入文本；逆操作删除同长度文本 */
export function insert(
  pos: number,
  text: string,
  expectedVersion?: number,
): Command<TextBuffer> {
  return {
    name: `insert(${pos}, "${text}")`,
    expectedVersion,
    execute(state) {
      if (pos < 0 || pos > state.text.length) {
        throw new RangeError(`插入位置越界: ${pos}`);
      }
      return {
        name: this.name,
        state: {
          text: state.text.slice(0, pos) + text + state.text.slice(pos),
          edits: state.edits + 1,
        },
        undo: (s) => ({
          text: s.text.slice(0, pos) + s.text.slice(pos + text.length),
          edits: s.edits - 1,
        }),
      };
    },
  };
}

/** 删除 [start, end)；逆操作恢复被删内容 */
export function remove(
  start: number,
  end: number,
  expectedVersion?: number,
): Command<TextBuffer> {
  return {
    name: `remove(${start}, ${end})`,
    expectedVersion,
    execute(state) {
      if (start < 0 || end > state.text.length || start > end) {
        throw new RangeError(`删除区间非法: [${start}, ${end})`);
      }
      const removed = state.text.slice(start, end);
      return {
        name: this.name,
        state: {
          text: state.text.slice(0, start) + state.text.slice(end),
          edits: state.edits + 1,
        },
        undo: (s) => ({
          text: s.text.slice(0, start) + removed + s.text.slice(start),
          edits: s.edits - 1,
        }),
      };
    },
  };
}

/** 替换 [start, end) 为 text；逆操作还原原文 */
export function replace(
  start: number,
  end: number,
  text: string,
  expectedVersion?: number,
): Command<TextBuffer> {
  return {
    name: `replace(${start}, ${end}, "${text}")`,
    expectedVersion,
    execute(state) {
      if (start < 0 || end > state.text.length || start > end) {
        throw new RangeError(`替换区间非法: [${start}, ${end})`);
      }
      const original = state.text.slice(start, end);
      return {
        name: this.name,
        state: {
          text: state.text.slice(0, start) + text + state.text.slice(end),
          edits: state.edits + 1,
        },
        undo: (s) => ({
          text: s.text.slice(0, start) + original + s.text.slice(start + text.length),
          edits: s.edits - 1,
        }),
      };
    },
  };
}

/** 测试辅助：必失败的命令 */
export function failing(name = "failing"): Command<TextBuffer> {
  return {
    name,
    execute() {
      throw new Error(`命令 "${name}" 执行失败`);
    },
  };
}
