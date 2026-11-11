import { copyMessage, type CopyOptions } from './copy.ts';

export type SendCallback<T> = (message: T) => void;

export class SecureMessenger {
  readonly #options: CopyOptions;

  constructor(options: CopyOptions = {}) {
    this.#options = options;
  }

  send<T>(message: T, sendCallback: SendCallback<T>): void {
    const safeCopy = copyMessage(message, this.#options);
    sendCallback(safeCopy);
  }

  copy<T>(message: T): T {
    return copyMessage(message, this.#options);
  }
}
