/** 验证阶段检测到 ReadSet 版本过期时抛出的冲突错误。 */
export class OccConflictError<K> extends Error {
  readonly conflictingKeys: readonly K[];

  constructor(conflictingKeys: readonly K[]) {
    super(
      `OCC validation failed: read set was invalidated by committed transactions on keys [${
        conflictingKeys.map(String).join(', ')
      }]`,
    );
    this.name = 'OccConflictError';
    this.conflictingKeys = conflictingKeys;
  }
}
