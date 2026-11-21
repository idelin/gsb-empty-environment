/**
 * 数据分级定义。
 *
 * 级别数字越大，敏感程度越高，可见范围越小。
 * 采用 `as const` 联合类型而非 enum，保证仅依赖类型擦除即可运行。
 */
export const Level = {
  PUBLIC: 0,
  INTERNAL: 1,
  CONFIDENTIAL: 2,
  SECRET: 3,
} as const;

export type Level = (typeof Level)[keyof typeof Level];

export type LevelName = 'PUBLIC' | 'INTERNAL' | 'CONFIDENTIAL' | 'SECRET';

const NAME_BY_LEVEL: Readonly<Record<number, LevelName>> = {
  [Level.PUBLIC]: 'PUBLIC',
  [Level.INTERNAL]: 'INTERNAL',
  [Level.CONFIDENTIAL]: 'CONFIDENTIAL',
  [Level.SECRET]: 'SECRET',
};

export function isLevel(value: unknown): value is Level {
  return (
    typeof value === 'number' &&
    Object.prototype.hasOwnProperty.call(NAME_BY_LEVEL, value)
  );
}

export function levelName(level: Level): LevelName {
  return NAME_BY_LEVEL[level];
}
