import {
  createTrustedCancel,
  createTrustedSelection,
  type TrustedPickerInput,
} from './trustedEvents.ts';
import type { SelectionSession } from './session.ts';
import type { StoredFileObject } from './types.ts';

/**
 * Trusted-side controller that stands in for the browser file chooser.
 * Only this host can produce branded events; page scripts calling the
 * session directly with hand-built objects get UntrustedEventError.
 */
export class TrustedFilePicker {
  #selectionId = 0;

  /** User confirmed a (possibly multi-file) selection. */
  confirmSelection(session: SelectionSession, files: readonly StoredFileObject[]): void {
    this.#selectionId += 1;
    const input: TrustedPickerInput = createTrustedSelection(this.#selectionId, files);
    session.handleTrustedPickerEvent(input);
  }

  /** User dismissed the dialog. The current ticket set stays unchanged. */
  cancel(session: SelectionSession): void {
    this.#selectionId += 1;
    const input: TrustedPickerInput = createTrustedCancel(this.#selectionId);
    session.handleTrustedPickerEvent(input);
  }
}
