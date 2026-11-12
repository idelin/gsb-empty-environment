import { FIELD_MAX_VALUE, FILTER_FIELDS } from './constants.ts';
import type { DeviceFilter } from './types.ts';

function isNonNegativeInteger(value: unknown): value is number {
  return (
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value >= 0
  );
}

function assertFieldInRange(
  field: (typeof FILTER_FIELDS)[number],
  value: number,
): void {
  const max = FIELD_MAX_VALUE[field];
  if (value > max) {
    throw new RangeError(
      `过滤器字段 ${field} 取值 ${value} 超出上限 0x${max
        .toString(16)
        .toUpperCase()}`,
    );
  }
}

/**
 * 校验单个过滤器：至少包含一个受支持字段，且每个字段都是合法整数。
 * 空对象（宽松过滤器）直接拒绝，避免攻击者用无约束条件索取敏感设备。
 */
export function validateFilter(filter: DeviceFilter): void {
  if (filter === null || typeof filter !== 'object') {
    throw new TypeError('过滤器必须是对象');
  }

  const presentFields = FILTER_FIELDS.filter(
    (field) => filter[field] !== undefined,
  );

  if (presentFields.length === 0) {
    throw new TypeError(
      '空过滤器被拒绝：过滤器必须至少包含 vendorId、productId、classCode、usagePage 之一',
    );
  }

  for (const field of presentFields) {
    const value = filter[field];
    if (!isNonNegativeInteger(value)) {
      throw new TypeError(
        `过滤器字段 ${field} 必须是非负整数，实际收到 ${String(value)}`,
      );
    }
    assertFieldInRange(field, value);
  }
}

/**
 * 校验一组过滤器。
 * @param filters 过滤器数组。
 * @param label 用于错误信息的参数名。
 * @param allowEmptyArray 是否允许空数组；包含过滤器不允许，排除过滤器允许省略/为空。
 */
export function validateFilterList(
  filters: DeviceFilter[] | undefined,
  label: string,
  allowEmptyArray: boolean,
): void {
  if (filters === undefined) {
    if (allowEmptyArray) return;
    throw new TypeError(`${label} 不能为空`);
  }

  if (!Array.isArray(filters)) {
    throw new TypeError(`${label} 必须是数组`);
  }

  if (filters.length === 0 && !allowEmptyArray) {
    throw new TypeError(`${label} 不能为空数组`);
  }

  filters.forEach((filter, index) => {
    try {
      validateFilter(filter);
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      throw new TypeError(`${label}[${index}] 校验失败：${reason}`);
    }
  });
}
