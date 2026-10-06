import {
  clampCodePointOffset,
  codePointLength,
  sliceByCodePoints,
  truncateToCodePoints,
} from "./unicode";

/**
 * 编辑控件状态机。
 *
 * 设计要点：
 * - 控件接收 composition / input / selection 三类事件；
 *   input 事件携带控件当前 value（如同在 DOM 处理器里读取 event.target.value），
 *   状态机据此维护“已提交视图”，因此重复投递的末次 input 天然幂等。
 * - 输入法组合（composition）期间：不截断文本、不提交校验、不产生撤销记录。
 * - compositionend 时统一应用长度上限（按 Unicode 码点计数）、提交校验，
 *   并且整个组合只压入一条撤销记录 —— 撤销直接恢复组合前状态。
 * - 取消组合（Escape，compositionend 的 data 为空且控件值已还原）时：
 *   恢复组合前的文本与选区，不产生撤销记录、不提交校验。
 * - 所有位置（选区、偏移）均以 Unicode 码点为单位；
 *   接入真实 DOM 时由适配层负责 UTF-16 code unit 与码点的换算。
 */

export type InputType =
  | "insertText"
  | "insertCompositionText"
  | "insertReplacementText"
  | "insertFromPaste"
  | "deleteContentBackward"
  | "deleteContentForward"
  | "deleteByCut";

/** 一次 input 事件：值为控件当前内容，选区为码点偏移。 */
export interface MachineInputEvent {
  readonly inputType: InputType;
  readonly value: string;
  readonly selectionStart: number;
  readonly selectionEnd: number;
  /** 对应 DOM InputEvent.isComposing；组合期间的 input 为 true。 */
  readonly isComposing?: boolean;
}

/** 可撤销/恢复的状态快照。 */
export interface Snapshot {
  readonly text: string;
  readonly selectionStart: number;
  readonly selectionEnd: number;
}

export interface EditorOptions {
  /** 允许的最大码点数；缺省表示不限制。 */
  readonly maxLength?: number;
  /** 提交校验：返回错误信息或 null。组合期间不会被调用。 */
  readonly validate?: (text: string) => string | null;
}

type Phase = "idle" | "composing";

function sameSnapshot(a: Snapshot, b: Snapshot): boolean {
  return (
    a.text === b.text &&
    a.selectionStart === b.selectionStart &&
    a.selectionEnd === b.selectionEnd
  );
}

export class EditorMachine {
  private textValue = "";
  private selStart = 0;
  private selEnd = 0;
  private phase: Phase = "idle";
  private preComposition: Snapshot | null = null;
  private compositionDataValue = "";
  private readonly undoStack: Snapshot[] = [];
  private error: string | null = null;
  private readonly maxLength?: number;
  private readonly validate?: (text: string) => string | null;

  constructor(options: EditorOptions = {}) {
    if (
      options.maxLength !== undefined &&
      (!Number.isInteger(options.maxLength) || options.maxLength < 0)
    ) {
      throw new RangeError(`maxLength 必须是非负整数，收到: ${options.maxLength}`);
    }
    this.maxLength = options.maxLength;
    this.validate = options.validate;
  }

  get text(): string {
    return this.textValue;
  }

  get selectionStart(): number {
    return this.selStart;
  }

  get selectionEnd(): number {
    return this.selEnd;
  }

  get isComposing(): boolean {
    return this.phase === "composing";
  }

  /** 组合期间的组合串（compositionupdate 的 data），组合外为空串。 */
  get compositionData(): string {
    return this.compositionDataValue;
  }

  /** 最近一次提交校验的结果；组合期间保持组合前的值不变。 */
  get validationError(): string | null {
    return this.error;
  }

  get canUndo(): boolean {
    return this.undoStack.length > 0;
  }

  /** 撤销栈深度（主要用于测试核对撤销记录数量）。 */
  get undoDepth(): number {
    return this.undoStack.length;
  }

  handleCompositionStart(): void {
    if (this.phase === "composing") {
      return; // 忽略嵌套的 compositionstart
    }
    this.phase = "composing";
    this.preComposition = this.snapshot();
    this.compositionDataValue = "";
  }

  handleCompositionUpdate(data: string): void {
    if (this.phase !== "composing") {
      return;
    }
    this.compositionDataValue = data;
  }

  handleCompositionEnd(data: string): void {
    if (this.phase !== "composing") {
      return;
    }
    this.phase = "idle";
    const pre = this.preComposition;
    this.preComposition = null;
    this.compositionDataValue = "";
    if (pre === null) {
      return;
    }

    // 取消组合：浏览器已把控件值还原为组合前内容（data 为空串），
    // 恢复组合前的文本与选区，不记撤销、不提交校验。
    const cancelled = data === "" && this.textValue === pre.text;
    if (cancelled) {
      this.textValue = pre.text;
      this.selStart = pre.selectionStart;
      this.selEnd = pre.selectionEnd;
      return;
    }

    // 提交：用 compositionend 的 data 替换实时文本中的组合段。
    // Chrome 在 compositionend 之前不会派发携带最终串的 input，
    // 因此不能依赖实时文本已包含提交内容。
    // 组合段范围由组合前快照推算：组合开始时浏览器删除选区并在此插入组合串，
    // 组合段之后缀（组合前文本中 caret 之后的部分）在组合期间不变。
    const segmentStart = pre.selectionStart;
    const baseLength =
      codePointLength(pre.text) - (pre.selectionEnd - pre.selectionStart);
    const suffixLength = baseLength - segmentStart;
    const liveLength = codePointLength(this.textValue);
    const segmentEnd = Math.max(
      segmentStart,
      Math.min(liveLength, liveLength - suffixLength),
    );
    const committed =
      sliceByCodePoints(this.textValue, 0, segmentStart) +
      data +
      sliceByCodePoints(this.textValue, segmentEnd);

    // 统一在此刻应用长度上限并提交校验；
    // 整个组合只压入一条撤销记录，撤销即恢复组合前状态。
    this.textValue = this.applyLimit(committed);
    this.selStart = segmentStart + codePointLength(data);
    this.selEnd = this.selStart;
    this.clampSelection();
    if (!sameSnapshot(pre, this.snapshot())) {
      this.undoStack.push(pre);
    }
    this.commitValidation();
  }

  handleInput(event: MachineInputEvent): void {
    // 组合期间（含错过 compositionstart、仅靠 isComposing 识别的情况）：
    // 只同步实时文本与选区，不截断、不校验、不记撤销。
    if (this.phase === "composing" || event.isComposing === true) {
      this.textValue = event.value;
      this.setSelection(event.selectionStart, event.selectionEnd);
      return;
    }

    // 重复的末次 input（如 Chrome 在 compositionend 之后以相同内容再发一次）：
    // 值未变化，幂等处理，仅同步选区。
    if (event.value === this.textValue) {
      this.setSelection(event.selectionStart, event.selectionEnd);
      return;
    }

    // 组合提交事件的重复投递，但携带的是截断回写前的值：
    // 采纳并截断，但不重复产生撤销记录与校验。
    if (event.inputType === "insertCompositionText") {
      this.textValue = this.applyLimit(event.value);
      this.setSelection(event.selectionStart, event.selectionEnd);
      return;
    }

    // 普通输入（含选区替换、粘贴、删除）：记撤销、截断、提交校验。
    this.undoStack.push(this.snapshot());
    this.textValue = this.applyLimit(event.value);
    this.setSelection(event.selectionStart, event.selectionEnd);
    this.commitValidation();
  }

  handleSelectionChange(start: number, end: number): void {
    this.setSelection(start, end);
  }

  /** 撤销一步；组合期间忽略。组合提交后的撤销直接恢复组合前状态。 */
  undo(): boolean {
    if (this.phase === "composing") {
      return false;
    }
    const snapshot = this.undoStack.pop();
    if (snapshot === undefined) {
      return false;
    }
    this.textValue = snapshot.text;
    this.selStart = snapshot.selectionStart;
    this.selEnd = snapshot.selectionEnd;
    this.commitValidation();
    return true;
  }

  private snapshot(): Snapshot {
    return {
      text: this.textValue,
      selectionStart: this.selStart,
      selectionEnd: this.selEnd,
    };
  }

  private applyLimit(text: string): string {
    return this.maxLength === undefined
      ? text
      : truncateToCodePoints(text, this.maxLength);
  }

  private commitValidation(): void {
    this.error = this.validate === undefined ? null : this.validate(this.textValue);
  }

  private setSelection(start: number, end: number): void {
    const clampedStart = clampCodePointOffset(this.textValue, start);
    const clampedEnd = clampCodePointOffset(this.textValue, end);
    this.selStart = Math.min(clampedStart, clampedEnd);
    this.selEnd = Math.max(clampedStart, clampedEnd);
  }

  private clampSelection(): void {
    this.setSelection(this.selStart, this.selEnd);
  }
}
