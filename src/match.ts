import type { DeviceFilter, DeviceLike } from './types.ts';

function matchesScalar(
  expected: number | undefined,
  actual: number | undefined,
): boolean {
  if (expected === undefined) return true;
  return actual === expected;
}

/**
 * 判断单个设备是否满足单个过滤器。
 * 过滤器中出现的每个字段都必须与设备对应值相等（字段间为“与”）；
 * usagePage 同时匹配设备顶层与任一集合，classCode 同时匹配设备与任一接口。
 */
export function matchesFilter(
  device: DeviceLike,
  filter: DeviceFilter,
): boolean {
  if (!matchesScalar(filter.vendorId, device.vendorId)) return false;
  if (!matchesScalar(filter.productId, device.productId)) return false;

  if (filter.usagePage !== undefined) {
    const pages = [device.usagePage];
    for (const collection of device.collections ?? []) {
      pages.push(collection.usagePage);
    }
    if (!pages.includes(filter.usagePage)) return false;
  }

  if (filter.classCode !== undefined) {
    const codes = [device.classCode];
    for (const iface of device.interfaces ?? []) {
      codes.push(iface.classCode);
    }
    if (!codes.includes(filter.classCode)) return false;
  }

  return true;
}

/** 设备是否匹配过滤器列表中的任意一个（列表间为“或”）。 */
export function matchesAnyFilter(
  device: DeviceLike,
  filters: DeviceFilter[],
): boolean {
  return filters.some((filter) => matchesFilter(device, filter));
}
