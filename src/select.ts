import { matchesAnyFilter } from './match.ts';
import { isProtectedDevice, isProtectedFilter } from './protected.ts';
import { validateFilterList } from './validate.ts';
import type {
  DeviceLike,
  DeviceSelectionOptions,
  SafeRequestOptions,
} from './types.ts';

/**
 * 从候选设备中执行安全选择：
 * 1. 拒绝空/宽松过滤器；
 * 2. 受保护设备（FIDO、键盘、存储接口）永不列出；
 * 3. 至少匹配一个包含过滤器；
 * 4. 命中任一排除过滤器的设备被剔除。
 *
 * @returns 通过全部规则的设备，保持输入顺序。
 */
export function selectDevices(
  devices: DeviceLike[],
  options: DeviceSelectionOptions,
): DeviceLike[] {
  validateFilterList(options.filters, 'filters', false);
  validateFilterList(
    options.exclusionFilters,
    'exclusionFilters',
    true,
  );

  const exclusionFilters = options.exclusionFilters ?? [];

  return devices.filter((device) => {
    if (isProtectedDevice(device)) return false;
    if (!matchesAnyFilter(device, options.filters)) return false;
    if (matchesAnyFilter(device, exclusionFilters)) return false;
    return true;
  });
}

/**
 * 构造可传给浏览器原生 requestDevice（如 navigator.hid.requestDevice）的
 * 安全过滤器参数。除结构校验外，还会拦截显式指向受保护用法页/类代码的过滤器。
 */
export function buildSafeRequestOptions(
  options: DeviceSelectionOptions,
): SafeRequestOptions {
  validateFilterList(options.filters, 'filters', false);
  validateFilterList(
    options.exclusionFilters,
    'exclusionFilters',
    true,
  );

  const guard = (
    filters: NonNullable<DeviceSelectionOptions['filters']>,
    label: string,
  ) => {
    if (filters.some((filter) => isProtectedFilter(filter))) {
      throw new TypeError(
        `${label} 指向受保护的用法页或类代码（FIDO/键盘/存储），禁止请求`,
      );
    }
  };

  guard(options.filters, 'filters');
  const exclusionFilters = options.exclusionFilters ?? [];
  guard(exclusionFilters, 'exclusionFilters');

  return {
    filters: options.filters.map((filter) => ({ ...filter })),
    exclusionFilters: exclusionFilters.map((filter) => ({ ...filter })),
  };
}
