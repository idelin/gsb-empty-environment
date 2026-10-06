import { describe, expect, it } from "bun:test";
import { EditorMachine } from "../src/editor-machine";
import { codePointLength, truncateToCodePoints } from "../src/unicode";
import { SyntheticControl, type BrowserOrder } from "./synthetic-control";

const ORDERS: BrowserOrder[] = ["chrome", "firefox"];

function makeControl(
  options: { maxLength?: number; validate?: (text: string) => string | null },
  order: BrowserOrder = "chrome",
) {
  const machine = new EditorMachine(options);
  const control = new SyntheticControl(machine, order);
  return { machine, control };
}

describe("Unicode 码点计数", () => {
  it("代理对按 1 个码点计数", () => {
    expect(codePointLength("😀")).toBe(1);
    expect("😀".length).toBe(2); // UTF-16 视角为 2，形成对照
    expect(codePointLength("a😀b🇨🇳")).toBe(5); // 🇨🇳 是 2 个码点
  });

  it("截断不拆散代理对", () => {
    expect(truncateToCodePoints("a😀b🇨🇳c", 3)).toBe("a😀b");
    expect(truncateToCodePoints("😀😀😀😀", 3)).toBe("😀😀😀");
    expect(truncateToCodePoints("abc", 0)).toBe("");
    expect(truncateToCodePoints("abc", 10)).toBe("abc");
  });
});

describe("组合期间保护", () => {
  for (const order of ORDERS) {
    it(`组合期间不截断不校验，组合结束统一应用上限（${order} 顺序）`, () => {
      let validateCalls = 0;
      const { machine, control } = makeControl(
        {
          maxLength: 5,
          validate: (text) => {
            validateCalls += 1;
            return text.includes("坏") ? "包含违禁词" : null;
          },
        },
        order,
      );

      control.type("ab");
      expect(validateCalls).toBe(1);

      control.compositionStart();
      control.compositionUpdate("你好");
      control.compositionUpdate("你好世界啊哈哈"); // 组合中达到 2 + 7 = 9 个码点

      // 组合期间：不截断、不提交校验、不记撤销
      expect(machine.isComposing).toBe(true);
      expect(machine.text).toBe("ab你好世界啊哈哈");
      expect(codePointLength(machine.text)).toBe(9);
      expect(machine.compositionData).toBe("你好世界啊哈哈");
      expect(validateCalls).toBe(1);
      expect(machine.validationError).toBeNull();
      expect(machine.undoDepth).toBe(1); // 只有 "ab" 那一条

      control.compositionEnd("你好世界啊哈哈坏");

      // 组合结束：统一截断到 5 个码点、提交一次校验
      expect(machine.isComposing).toBe(false);
      expect(machine.text).toBe("ab你好世");
      expect(codePointLength(machine.text)).toBe(5);
      expect(validateCalls).toBe(2);
      expect(machine.validationError).toBeNull();
      expect(machine.undoDepth).toBe(2); // 整个组合只记一条
    });
  }

  it("组合结束提交违禁词时校验生效", () => {
    const { machine, control } = makeControl({
      validate: (text) => (text.includes("坏") ? "包含违禁词" : null),
    });
    control.compositionStart();
    control.compositionUpdate("huai");
    control.compositionEnd("坏");
    expect(machine.text).toBe("坏");
    expect(machine.validationError).toBe("包含违禁词");
  });

  it("组合期间校验错误保持组合前的已提交值", () => {
    const { machine, control } = makeControl({
      validate: (text) => (text.includes("坏") ? "包含违禁词" : null),
    });
    control.type("坏");
    expect(machine.validationError).toBe("包含违禁词");
    control.select(0, 1); // 选中 "坏"，组合将替换它
    control.compositionStart();
    control.compositionUpdate("hao");
    // 组合中的实时文本已不含 "坏"……但校验不提交，错误保持
    control.compositionUpdate("好");
    expect(machine.text).toBe("好");
    expect(machine.validationError).toBe("包含违禁词");
    control.compositionEnd("好");
    expect(machine.text).toBe("好");
    expect(machine.validationError).toBeNull();
  });
});

describe("重复末次 input", () => {
  it("Chrome 顺序：compositionend 后的重复 input 幂等", () => {
    let validateCalls = 0;
    const { machine, control } = makeControl(
      { maxLength: 10, validate: () => { validateCalls += 1; return null; } },
      "chrome",
    );
    control.type("ab");
    control.compositionStart();
    control.compositionUpdate("ce");
    control.compositionEnd("词"); // 内部会再派发一次相同的末次 input

    expect(machine.text).toBe("ab词");
    expect(machine.undoDepth).toBe(2); // 重复 input 不产生额外撤销记录
    expect(validateCalls).toBe(2); // 重复 input 不重复提交校验

    expect(machine.undo()).toBe(true);
    expect(machine.text).toBe("ab"); // 一次撤销直接回到组合前
  });

  it("重复 input 携带截断回写前的值时不重复记撤销", () => {
    const machine = new EditorMachine({ maxLength: 3 });
    machine.handleCompositionStart();
    machine.handleInput({
      inputType: "insertCompositionText",
      value: "abcdef",
      selectionStart: 6,
      selectionEnd: 6,
      isComposing: true,
    });
    machine.handleCompositionEnd("abcdef");
    expect(machine.text).toBe("abc");
    expect(machine.undoDepth).toBe(1);

    // 适配层尚未回写截断值，重复事件仍携带 "abcdef"
    machine.handleInput({
      inputType: "insertCompositionText",
      value: "abcdef",
      selectionStart: 6,
      selectionEnd: 6,
      isComposing: false,
    });
    expect(machine.text).toBe("abc"); // 采纳后同样被截断
    expect(machine.undoDepth).toBe(1); // 不重复记撤销
    expect(machine.selectionStart).toBe(3); // 选区夹取到截断后的文本内

    expect(machine.undo()).toBe(true);
    expect(machine.text).toBe("");
  });

  it("Firefox 顺序：末次 input 先于 compositionend，结果一致", () => {
    const { machine, control } = makeControl({ maxLength: 10 }, "firefox");
    control.type("ab");
    control.compositionStart();
    control.compositionUpdate("ci");
    control.compositionEnd("词");
    expect(machine.text).toBe("ab词");
    expect(machine.undoDepth).toBe(2);
    expect(machine.undo()).toBe(true);
    expect(machine.text).toBe("ab");
  });
});

describe("取消组合", () => {
  for (const order of ORDERS) {
    it(`Escape 取消后恢复组合前状态（${order} 顺序）`, () => {
      let validateCalls = 0;
      const { machine, control } = makeControl(
        { maxLength: 10, validate: () => { validateCalls += 1; return null; } },
        order,
      );
      control.type("hello");
      control.select(1, 4); // 选中 "ell"
      const depthBefore = machine.undoDepth;
      const callsBefore = validateCalls;

      control.compositionStart();
      control.compositionUpdate("pin");
      control.compositionUpdate("pin yin");
      expect(machine.isComposing).toBe(true);

      control.compositionCancel();

      expect(machine.isComposing).toBe(false);
      expect(machine.text).toBe("hello"); // 文本还原
      expect(machine.selectionStart).toBe(1); // 选区还原
      expect(machine.selectionEnd).toBe(4);
      expect(machine.undoDepth).toBe(depthBefore); // 不产生撤销记录
      expect(validateCalls).toBe(callsBefore); // 不提交校验
    });
  }

  it("取消后 undo 仍然回到组合之前的更早状态", () => {
    const { machine, control } = makeControl({ maxLength: 10 });
    control.type("abc");
    control.compositionStart();
    control.compositionUpdate("x");
    control.compositionCancel();
    expect(machine.text).toBe("abc");
    expect(machine.undo()).toBe(true);
    expect(machine.text).toBe("");
  });
});

describe("选区替换", () => {
  it("键入替换选区并折叠光标", () => {
    const { machine, control } = makeControl({});
    control.type("hello world");
    control.select(6, 11); // 选中 "world"
    control.type("朋友");
    expect(machine.text).toBe("hello 朋友");
    expect(machine.selectionStart).toBe(8);
    expect(machine.selectionEnd).toBe(8);
  });

  it("替换结果超限时按码点截断", () => {
    const { machine, control } = makeControl({ maxLength: 8 });
    control.type("hello world");
    control.select(6, 11);
    control.type("朋友朋友朋友"); // "hello "（6 码点）+ 6 码点 -> 截断为 8 码点
    expect(machine.text).toBe("hello 朋友");
    expect(codePointLength(machine.text)).toBe(8);
  });

  it("替换中的 emoji 按码点计数", () => {
    const { machine, control } = makeControl({ maxLength: 4 });
    control.type("ab");
    control.select(0, 2);
    control.type("😀😀😀😀😀"); // 5 个码点 -> 截断为 4 个
    expect(machine.text).toBe("😀😀😀😀");
    expect(codePointLength(machine.text)).toBe(4);
  });

  it("撤销恢复替换前的文本与选区", () => {
    const { machine, control } = makeControl({});
    control.type("hello world");
    control.select(6, 11);
    control.type("朋友");
    expect(machine.undo()).toBe(true);
    expect(machine.text).toBe("hello world");
    expect(machine.selectionStart).toBe(6);
    expect(machine.selectionEnd).toBe(11);
  });

  it("组合替换选区：提交后撤销恢复组合前状态", () => {
    const { machine, control } = makeControl({ maxLength: 20 });
    control.type("hello world");
    control.select(6, 11); // 选中 "world"
    control.compositionStart();
    control.compositionUpdate("peng");
    control.compositionEnd("朋友");
    expect(machine.text).toBe("hello 朋友");
    expect(machine.undoDepth).toBe(2);
    expect(machine.undo()).toBe(true);
    expect(machine.text).toBe("hello world");
    expect(machine.selectionStart).toBe(6);
    expect(machine.selectionEnd).toBe(11);
  });
});

describe("撤销链", () => {
  it("多次组合逐次撤销", () => {
    const { machine, control } = makeControl({ maxLength: 20 });
    control.compositionStart();
    control.compositionUpdate("ni");
    control.compositionEnd("你");
    control.compositionStart();
    control.compositionUpdate("hao");
    control.compositionEnd("好");
    expect(machine.text).toBe("你好");
    expect(machine.undoDepth).toBe(2);

    expect(machine.undo()).toBe(true);
    expect(machine.text).toBe("你");
    expect(machine.undo()).toBe(true);
    expect(machine.text).toBe("");
    expect(machine.undo()).toBe(false);
  });

  it("组合期间 undo 被忽略", () => {
    const { machine, control } = makeControl({});
    control.type("ab");
    control.compositionStart();
    control.compositionUpdate("x");
    expect(machine.undo()).toBe(false);
    expect(machine.text).toBe("abx");
    control.compositionEnd("x");
    expect(machine.text).toBe("abx");
  });
});

describe("边界行为", () => {
  it("错过 compositionstart 时 isComposing 标记仍保护组合输入", () => {
    const machine = new EditorMachine({ maxLength: 3 });
    machine.handleInput({
      inputType: "insertCompositionText",
      value: "abcdef",
      selectionStart: 6,
      selectionEnd: 6,
      isComposing: true,
    });
    expect(machine.text).toBe("abcdef"); // 不截断
    expect(machine.undoDepth).toBe(0);
  });

  it("选区偏移越界时夹取到文本范围内", () => {
    const { machine, control } = makeControl({});
    control.type("abc");
    machine.handleSelectionChange(2, 99);
    expect(machine.selectionStart).toBe(2);
    expect(machine.selectionEnd).toBe(3);
  });

  it("退格删除按码点进行", () => {
    const { machine, control } = makeControl({});
    control.type("a😀b");
    control.backspace();
    expect(machine.text).toBe("a😀");
    control.backspace();
    expect(machine.text).toBe("a");
  });

  it("非法 maxLength 抛错", () => {
    expect(() => new EditorMachine({ maxLength: -1 })).toThrow(RangeError);
    expect(() => new EditorMachine({ maxLength: 1.5 })).toThrow(RangeError);
  });
});
