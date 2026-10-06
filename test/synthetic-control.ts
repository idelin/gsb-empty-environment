import { EditorMachine } from "../src/editor-machine";
import { codePointLength } from "../src/unicode";

/**
 * 合成控件：模拟浏览器/框架侧的行为，用于事件重放测试。
 *
 * - 维护控件的原始 value 与选区（码点偏移），按浏览器语义派发
 *   composition / input / selection 事件给状态机；
 * - 支持两种真实浏览器的事件顺序：
 *   - "chrome"：compositionend 之后再派发一次内容相同的末次 input；
 *   - "firefox"：末次 input 先于 compositionend 派发；
 * - syncFromMachine 模拟框架适配层：把状态机截断后的文本回写控件。
 */

export type BrowserOrder = "chrome" | "firefox";

function insertAt(base: string, offsetCp: number, insert: string): string {
  const chars = [...base];
  return chars.slice(0, offsetCp).join("") + insert + chars.slice(offsetCp).join("");
}

function deleteRange(base: string, startCp: number, endCp: number): string {
  const chars = [...base];
  return chars.slice(0, startCp).join("") + chars.slice(endCp).join("");
}

export class SyntheticControl {
  value = "";
  selectionStart = 0;
  selectionEnd = 0;

  private baseValue = "";
  private baseCaret = 0;
  private preValue = "";
  private preSelStart = 0;
  private preSelEnd = 0;

  constructor(
    private readonly machine: EditorMachine,
    private readonly order: BrowserOrder = "chrome",
  ) {}

  /** 用户改变选区（码点偏移）。 */
  select(start: number, end: number): void {
    this.selectionStart = start;
    this.selectionEnd = end;
    this.machine.handleSelectionChange(start, end);
  }

  /** 普通键入：替换当前选区。 */
  type(text: string): void {
    this.replaceSelectionWith(text);
    this.machine.handleInput({
      inputType: "insertText",
      value: this.value,
      selectionStart: this.selectionStart,
      selectionEnd: this.selectionEnd,
      isComposing: false,
    });
    this.syncFromMachine();
  }

  /** 退格：有选区删选区，否则删前一个码点。 */
  backspace(): void {
    if (this.selectionStart !== this.selectionEnd) {
      this.value = deleteRange(this.value, this.selectionStart, this.selectionEnd);
      this.selectionEnd = this.selectionStart;
    } else if (this.selectionStart > 0) {
      this.value = deleteRange(this.value, this.selectionStart - 1, this.selectionStart);
      this.selectionStart -= 1;
      this.selectionEnd = this.selectionStart;
    }
    this.machine.handleInput({
      inputType: "deleteContentBackward",
      value: this.value,
      selectionStart: this.selectionStart,
      selectionEnd: this.selectionEnd,
      isComposing: false,
    });
    this.syncFromMachine();
  }

  /** 输入法组合开始：浏览器先派发 compositionstart，再移除选区。 */
  compositionStart(): void {
    this.preValue = this.value;
    this.preSelStart = this.selectionStart;
    this.preSelEnd = this.selectionEnd;
    this.machine.handleCompositionStart();
    this.baseValue = deleteRange(this.value, this.selectionStart, this.selectionEnd);
    this.baseCaret = this.selectionStart;
  }

  /** 组合串更新：浏览器把组合段替换为最新拼音/候选，并派发 input(isComposing)。 */
  compositionUpdate(data: string): void {
    this.value = insertAt(this.baseValue, this.baseCaret, data);
    this.selectionStart = this.selectionEnd = this.baseCaret + codePointLength(data);
    this.machine.handleCompositionUpdate(data);
    this.machine.handleInput({
      inputType: "insertCompositionText",
      value: this.value,
      selectionStart: this.selectionStart,
      selectionEnd: this.selectionEnd,
      isComposing: true,
    });
  }

  /** 组合提交：按浏览器顺序派发 compositionend 与末次 input。 */
  compositionEnd(data: string): void {
    this.value = insertAt(this.baseValue, this.baseCaret, data);
    this.selectionStart = this.selectionEnd = this.baseCaret + codePointLength(data);
    if (this.order === "firefox") {
      // Firefox：末次 input 先于 compositionend。
      this.machine.handleInput({
        inputType: "insertCompositionText",
        value: this.value,
        selectionStart: this.selectionStart,
        selectionEnd: this.selectionEnd,
        isComposing: false,
      });
      this.machine.handleCompositionEnd(data);
      this.syncFromMachine();
    } else {
      // Chrome：compositionend 之后再发一次内容相同的 input（重复末次 input）。
      this.machine.handleCompositionEnd(data);
      this.syncFromMachine();
      this.machine.handleInput({
        inputType: "insertCompositionText",
        value: this.value,
        selectionStart: this.selectionStart,
        selectionEnd: this.selectionEnd,
        isComposing: false,
      });
    }
  }

  /** 取消组合（Escape）：浏览器先把值还原为组合前内容，再发 compositionend('')。 */
  compositionCancel(): void {
    this.value = this.preValue;
    this.selectionStart = this.preSelStart;
    this.selectionEnd = this.preSelEnd;
    this.machine.handleCompositionUpdate("");
    this.machine.handleInput({
      inputType: "insertCompositionText",
      value: this.value,
      selectionStart: this.selectionStart,
      selectionEnd: this.selectionEnd,
      isComposing: true,
    });
    this.machine.handleCompositionEnd("");
    if (this.order === "chrome") {
      this.machine.handleInput({
        inputType: "insertCompositionText",
        value: this.value,
        selectionStart: this.selectionStart,
        selectionEnd: this.selectionEnd,
        isComposing: false,
      });
    }
    this.syncFromMachine();
  }

  private replaceSelectionWith(text: string): void {
    this.value = deleteRange(this.value, this.selectionStart, this.selectionEnd);
    this.value = insertAt(this.value, this.selectionStart, text);
    this.selectionStart = this.selectionEnd =
      this.selectionStart + codePointLength(text);
  }

  private syncFromMachine(): void {
    if (this.machine.text !== this.value) {
      this.value = this.machine.text;
      const length = codePointLength(this.value);
      if (this.selectionStart > length) {
        this.selectionStart = this.selectionEnd = length;
      }
    }
  }
}
