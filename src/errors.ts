import { LevelSkip, LevelSkipFix } from './types';

/** 跳级违例：编辑被拒绝，文档保持不变，附带最小修复范围建议。 */
export class LevelSkipError extends Error {
  constructor(
    public readonly skips: LevelSkip[],
    public readonly fix: LevelSkipFix,
  ) {
    const first = skips[0];
    super(
      `Level skip at heading "${first.headingId}": ` +
        `level ${first.actualLevel} exceeds max allowed ${first.maxAllowedLevel}`,
    );
    this.name = 'LevelSkipError';
  }
}

export class HeadingNotFoundError extends Error {
  constructor(id: string) {
    super(`Heading not found: "${id}"`);
    this.name = 'HeadingNotFoundError';
  }
}

export class DuplicateHeadingIdError extends Error {
  constructor(id: string) {
    super(`Duplicate heading id: "${id}"`);
    this.name = 'DuplicateHeadingIdError';
  }
}

/** 移动目标位于被移动子树内部。 */
export class CyclicMoveError extends Error {
  constructor(id: string, parentId: string) {
    super(`Cannot move heading "${id}" into its own subtree ("${parentId}")`);
    this.name = 'CyclicMoveError';
  }
}

export class InvalidLevelError extends Error {
  constructor(level: number) {
    super(`Invalid level: ${level} (must be >= 1)`);
    this.name = 'InvalidLevelError';
  }
}

/** 按标题查找时命中多个同名标题。 */
export class AmbiguousTitleError extends Error {
  constructor(title: string, public readonly matchingIds: string[]) {
    super(`Title "${title}" matches ${matchingIds.length} headings: ${matchingIds.join(', ')}`);
    this.name = 'AmbiguousTitleError';
  }
}
