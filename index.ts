export {
  CLASS_CODE_MASS_STORAGE,
  FIELD_MAX_VALUE,
  FILTER_FIELDS,
  PROTECTED_CLASS_CODES,
  PROTECTED_USAGE_PAGES,
  USAGE_PAGE_FIDO,
  USAGE_PAGE_KEYBOARD,
} from './src/constants.ts';

export {
  matchesAnyFilter,
  matchesFilter,
} from './src/match.ts';

export {
  isProtectedDevice,
  isProtectedFilter,
} from './src/protected.ts';

export {
  buildSafeRequestOptions,
  selectDevices,
} from './src/select.ts';

export type {
  DeviceCollection,
  DeviceFilter,
  DeviceInterface,
  DeviceLike,
  DeviceSelectionOptions,
  SafeRequestOptions,
} from './src/types.ts';

export {
  validateFilter,
  validateFilterList,
} from './src/validate.ts';
