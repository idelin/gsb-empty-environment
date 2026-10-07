export type OverflowKind = 'header' | 'footnote' | 'group';

export interface OverflowInit {
  kind: OverflowKind;
  /** 放不下的元素所需高度。 */
  requiredHeight: number;
  /** 当前可用高度。 */
  availableHeight: number;
  /** kind 为 'group' 时，指明超页的具体行组。 */
  groupId?: string;
  groupIndex?: number;
}

/**
 * 排版溢出错误：行组（加表头）超过单页容量、表头占满整页、
 * 或尾注超过末页内容区时抛出。分页器保证在抛错前有界终止，
 * 不会为放不下的内容无限新建页面。
 */
export class PaginationOverflowError extends Error {
  readonly kind: OverflowKind;
  readonly requiredHeight: number;
  readonly availableHeight: number;
  readonly groupId: string | undefined;
  readonly groupIndex: number | undefined;

  constructor(init: OverflowInit) {
    super(describe(init));
    this.name = 'PaginationOverflowError';
    this.kind = init.kind;
    this.requiredHeight = init.requiredHeight;
    this.availableHeight = init.availableHeight;
    this.groupId = init.groupId;
    this.groupIndex = init.groupIndex;
  }
}

function describe(init: OverflowInit): string {
  switch (init.kind) {
    case 'header':
      return `表头高度 ${init.requiredHeight} 已占满整页（页高 ${init.availableHeight}），没有空间排版任何行组`;
    case 'footnote':
      return `尾注高度 ${init.requiredHeight} 超过末页内容区容量 ${init.availableHeight}（页高 - 表头），尾注无法放置`;
    case 'group':
      return (
        `行组 "${init.groupId}"（第 ${init.groupIndex} 组）高度 ${init.requiredHeight} ` +
        `超过单页内容区容量 ${init.availableHeight}（页高 - 表头）；` +
        `合并单元格行组不可拆分，请减小行高或增大页面`
      );
  }
}
