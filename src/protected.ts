import {
  PROTECTED_CLASS_CODES,
  PROTECTED_USAGE_PAGES,
} from './constants.ts';
import type { DeviceFilter, DeviceLike } from './types.ts';

function deviceUsagePages(device: DeviceLike): number[] {
  const pages: number[] = [];
  if (device.usagePage !== undefined) pages.push(device.usagePage);
  for (const collection of device.collections ?? []) {
    if (collection.usagePage !== undefined) pages.push(collection.usagePage);
  }
  return pages;
}

function deviceClassCodes(device: DeviceLike): number[] {
  const codes: number[] = [];
  if (device.classCode !== undefined) codes.push(device.classCode);
  for (const iface of device.interfaces ?? []) {
    if (iface.classCode !== undefined) codes.push(iface.classCode);
  }
  return codes;
}

/**
 * 判断设备是否受保护而不得列出：
 * - 设备或其任一集合命中受保护用法页（含 FIDO 0xF1D0、键盘 0x07）；
 * - 设备或其任一接口命中受保护类代码（存储接口 0x08）。
 */
export function isProtectedDevice(device: DeviceLike): boolean {
  if (
    deviceUsagePages(device).some((page) =>
      PROTECTED_USAGE_PAGES.includes(page),
    )
  ) {
    return true;
  }
  return deviceClassCodes(device).some((code) =>
    PROTECTED_CLASS_CODES.includes(code),
  );
}

/**
 * 判断过滤器本身是否指向受保护的用法页或类代码。
 * 用于在构造原生请求参数时拦截显式索取 FIDO/键盘/存储设备的过滤器。
 */
export function isProtectedFilter(filter: DeviceFilter): boolean {
  if (
    filter.usagePage !== undefined &&
    PROTECTED_USAGE_PAGES.includes(filter.usagePage)
  ) {
    return true;
  }
  return (
    filter.classCode !== undefined &&
    PROTECTED_CLASS_CODES.includes(filter.classCode)
  );
}
