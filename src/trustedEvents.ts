import { UntrustedEventError } from './errors.ts';
import type { StoredFileObject } from './types.ts';

/**
 * Non-exported brand: scripts cannot construct an object carrying it, so
 * they cannot forge trusted events. It mirrors the browser isTrusted flag.
 */
const trustedBrand = Symbol('trustedFilePickerEvent');

export interface TrustedSelectionInput {
  readonly [trustedBrand]: true;
  readonly kind: 'selection';
  readonly selectionId: number;
  readonly files: readonly StoredFileObject[];
}

export interface TrustedCancelInput {
  readonly [trustedBrand]: true;
  readonly kind: 'cancel';
  readonly selectionId: number;
}

export type TrustedPickerInput = TrustedSelectionInput | TrustedCancelInput;

export function assertTrusted(
  event: object,
): asserts event is TrustedPickerInput {
  if (!(trustedBrand in event) || (event as { [trustedBrand]?: unknown })[trustedBrand] !== true) {
    throw new UntrustedEventError();
  }
}

/**
 * Host-side constructor. It is intentionally not exported from the package
 * root as part of the script-facing API; the picker host uses it after a
 * real, user-initiated picker dialog completes.
 */
export function createTrustedSelection(
  selectionId: number,
  files: readonly StoredFileObject[],
): TrustedSelectionInput {
  return { [trustedBrand]: true, kind: 'selection', selectionId, files };
}

export function createTrustedCancel(selectionId: number): TrustedCancelInput {
  return { [trustedBrand]: true, kind: 'cancel', selectionId };
}
