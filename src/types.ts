/** 输入一个标题：id 可省略（由库生成稳定 ID），level 为 1 开始的绝对层级。 */
export interface HeadingInput {
  id?: string;
  title: string;
  level: number;
}

/** 文档内部的标题节点（扁平存储，树结构由 level 推导）。 */
export interface Heading {
  id: string;
  title: string;
  level: number;
}

/** 目录视图中的一行。 */
export interface OutlineItem extends Heading {
  number: string;
}

/** 一次编辑引起的编号变化；oldNumber/newNumber 为 null 分别表示新增/删除。 */
export interface NumberChange {
  id: string;
  title: string;
  oldNumber: string | null;
  newNumber: string | null;
}

/** 一处跳级违例：headingId 的层级超过了允许的最大层级。 */
export interface LevelSkip {
  headingId: string;
  maxAllowedLevel: number;
  actualLevel: number;
}

/** 对某个标题建议的层级调整量（正数升级，负数降级）。 */
export interface LevelAdjustment {
  id: string;
  delta: number;
}

/**
 * 跳级时的最小修复范围：只列出必须调整层级的标题，
 * 由调用方决定是否应用——库本身绝不擅自改正文。
 */
export interface LevelSkipFix {
  direction: 'lower-following' | 'raise-preceding';
  adjustments: LevelAdjustment[];
}

/** 外部引用：绑定稳定 ID，并保存创建时的编号/标题快照（旧版本显示用）。 */
export interface Reference {
  id: string;
  targetId: string;
  createdVersion: number;
  snapshotNumber: string;
  snapshotTitle: string;
}

/** 引用解析结果。 */
export interface ResolvedReference {
  reference: Reference;
  status: 'ok' | 'dangling';
  /** 目标当前的编号与标题；目标已删除时为 null。 */
  current: { number: string; title: string } | null;
  /** 当前显示与创建时快照是否不一致（含目标已删除）。 */
  stale: boolean;
  /** 建议显示文本：存活时显示当前编号，悬空时回退到旧版本快照并标注。 */
  display: string;
  /** 创建时的旧版本显示文本。 */
  snapshotDisplay: string;
}

export interface EditSuccess {
  ok: true;
  version: number;
  /** 本次编辑引起的全部编号变化（增量 diff）。 */
  changes: NumberChange[];
  /** 编辑后当前处于悬空状态的引用。 */
  danglingReferences: ResolvedReference[];
}

export interface EditFailure {
  ok: false;
  error: Error;
}

export type EditResult = EditSuccess | EditFailure;
