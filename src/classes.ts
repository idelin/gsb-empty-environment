import type { ProtectionClass } from './types.ts';

/**
 * 保护类强度表：数值越大越强。
 * complete(3) > completeUnlessOpen(2) >
 * completeUntilFirstUserAuthentication(1) > none(0)
 */
const STRENGTH: Record<ProtectionClass, number> = {
  none: 0,
  completeUntilFirstUserAuthentication: 1,
  completeUnlessOpen: 2,
  complete: 3,
};

/** 从强到弱排列的保护类，便于遍历与展示。 */
export const PROTECTION_CLASSES_ORDERED: readonly ProtectionClass[] = [
  'complete',
  'completeUnlessOpen',
  'completeUntilFirstUserAuthentication',
  'none',
];

/** 返回保护类的强度值，越大越强。 */
export function strengthOf(protectionClass: ProtectionClass): number {
  return STRENGTH[protectionClass];
}

/** `a` 是否严格强于 `b`。 */
export function isStronger(
  a: ProtectionClass,
  b: ProtectionClass,
): boolean {
  return STRENGTH[a] > STRENGTH[b];
}

/** `a` 是否弱于 `b`。 */
export function isWeaker(
  a: ProtectionClass,
  b: ProtectionClass,
): boolean {
  return STRENGTH[a] < STRENGTH[b];
}

/** `actual` 是否满足（不弱于）`required` 最低要求。 */
export function meetsRequirement(
  actual: ProtectionClass,
  required: ProtectionClass,
): boolean {
  return STRENGTH[actual] >= STRENGTH[required];
}

/** 返回两个类中更强的那个，用于多规则取最强要求。 */
export function strongerOf(
  a: ProtectionClass,
  b: ProtectionClass,
): ProtectionClass {
  return STRENGTH[a] >= STRENGTH[b] ? a : b;
}
