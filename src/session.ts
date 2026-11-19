import { ConnectionCapState } from './cap.ts';
import type { IrcMessage } from './parser.ts';

/**
 * Numeric 451 reply parameters returned when an unregistered client tries a
 * command it is not allowed to use yet.
 */
export interface RejectedCommand {
  numeric: 451;
  params: [string, string];
}

const REGISTRATION_COMMANDS = new Set(['NICK', 'USER', 'PASS', 'QUIT', 'CAP']);

/**
 * Enforces the registration state machine. While CAP LS 302 has suspended
 * registration, only registration commands and CAP itself are accepted; any
 * other command (e.g. PRIVMSG) is refused as if the client were unregistered.
 */
export class RegistrationGate {
  private registered = false;
  private readonly capState: ConnectionCapState;

  constructor(capState: ConnectionCapState) {
    this.capState = capState;
  }

  markRegistered(): void {
    this.registered = true;
  }

  isRegistered(): boolean {
    return this.registered;
  }

  /**
   * Returns a 451 rejection when the command must not run, or `undefined` when
   * the command is allowed at the current registration state.
   */
  check(message: IrcMessage): RejectedCommand | undefined {
    if (this.registered) {
      return undefined;
    }
    if (!this.capState.registrationSuspended) {
      // Pre-registration traffic is handled by the normal registration flow.
      return undefined;
    }
    if (REGISTRATION_COMMANDS.has(message.command)) {
      return undefined;
    }
    return { numeric: 451, params: ['*', 'You have not registered'] };
  }
}
