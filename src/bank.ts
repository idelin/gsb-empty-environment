import type { Derivation, Reducer } from './types.ts';

export interface BankState {
  readonly accounts: Readonly<Record<string, number>>;
}

export type BankCommand =
  | { readonly type: 'open'; readonly accountId: string }
  | { readonly type: 'deposit'; readonly accountId: string; readonly amount: number }
  | {
      readonly type: 'transfer';
      readonly transferId: string;
      readonly from: string;
      readonly to: string;
      readonly amount: number;
    };

export type BankEvent =
  | { readonly type: 'account-opened'; readonly accountId: string }
  | {
      readonly type: 'funds-deposited';
      readonly accountId: string;
      readonly amount: number;
    }
  | {
      readonly type: 'funds-debited';
      readonly accountId: string;
      readonly amount: number;
      readonly transferId: string;
    }
  | {
      readonly type: 'funds-credited';
      readonly accountId: string;
      readonly amount: number;
      readonly transferId: string;
    };

export type BankEffect =
  | { readonly type: 'POST /receipts/open'; readonly accountId: string }
  | {
      readonly type: 'POST /receipts/deposit';
      readonly accountId: string;
      readonly amount: number;
    }
  | {
      readonly type: 'POST /external/transfers';
      readonly transferId: string;
      readonly from: string;
      readonly to: string;
      readonly amount: number;
    };

export const initialBankState: BankState = Object.freeze({ accounts: Object.freeze({}) });

function assertMoney(amount: number): void {
  if (!Number.isFinite(amount) || amount <= 0) {
    throw new Error('Amount must be a positive number.');
  }
}

export const bankReducer: Reducer<BankState, BankEvent> = (state, event) => {
  switch (event.type) {
    case 'account-opened': {
      if (state.accounts[event.accountId] !== undefined) {
        throw new Error(`Account already exists: ${event.accountId}`);
      }
      return {
        accounts: Object.freeze({ ...state.accounts, [event.accountId]: 0 }),
      };
    }
    case 'funds-deposited': {
      if (state.accounts[event.accountId] === undefined) {
        throw new Error(`Unknown account: ${event.accountId}`);
      }
      return {
        accounts: Object.freeze({
          ...state.accounts,
          [event.accountId]: state.accounts[event.accountId] + event.amount,
        }),
      };
    }
    case 'funds-debited': {
      const balance = state.accounts[event.accountId];
      if (balance === undefined) throw new Error(`Unknown account: ${event.accountId}`);
      if (balance < event.amount) {
        throw new Error(`Insufficient funds: ${event.accountId}`);
      }
      return {
        accounts: Object.freeze({
          ...state.accounts,
          [event.accountId]: balance - event.amount,
        }),
      };
    }
    case 'funds-credited': {
      const balance = state.accounts[event.accountId];
      if (balance === undefined) throw new Error(`Unknown account: ${event.accountId}`);
      return {
        accounts: Object.freeze({
          ...state.accounts,
          [event.accountId]: balance + event.amount,
        }),
      };
    }
  }
};

export function handleBankCommand(
  command: BankCommand,
  context: { readonly state: BankState }
): { readonly events: readonly BankEvent[]; readonly effects: readonly BankEffect[] } {
  switch (command.type) {
    case 'open': {
      if (context.state.accounts[command.accountId] !== undefined) {
        throw new Error(`Account already exists: ${command.accountId}`);
      }
      return {
        events: [{ type: 'account-opened', accountId: command.accountId }],
        effects: [{ type: 'POST /receipts/open', accountId: command.accountId }],
      };
    }
    case 'deposit': {
      assertMoney(command.amount);
      if (context.state.accounts[command.accountId] === undefined) {
        throw new Error(`Unknown account: ${command.accountId}`);
      }
      return {
        events: [
          {
            type: 'funds-deposited',
            accountId: command.accountId,
            amount: command.amount,
          },
        ],
        effects: [
          {
            type: 'POST /receipts/deposit',
            accountId: command.accountId,
            amount: command.amount,
          },
        ],
      };
    }
    case 'transfer': {
      assertMoney(command.amount);
      const fromBalance = context.state.accounts[command.from];
      const toBalance = context.state.accounts[command.to];
      if (fromBalance === undefined || toBalance === undefined) {
        throw new Error('Both transfer accounts must exist.');
      }
      if (fromBalance < command.amount) {
        throw new Error(`Insufficient funds: ${command.from}`);
      }
      return {
        events: [
          {
            type: 'funds-debited',
            accountId: command.from,
            amount: command.amount,
            transferId: command.transferId,
          },
          {
            type: 'funds-credited',
            accountId: command.to,
            amount: command.amount,
            transferId: command.transferId,
          },
        ],
        effects: [
          {
            type: 'POST /external/transfers',
            transferId: command.transferId,
            from: command.from,
            to: command.to,
            amount: command.amount,
          },
        ],
      };
    }
  }
}

export const totalBalance: Derivation<BankState, BankEvent, BankEffect, number> = ({ state }) =>
  Object.values(state.accounts).reduce((sum, balance) => sum + balance, 0);

export const accountCount: Derivation<BankState, BankEvent, BankEffect, number> = ({ state }) =>
  Object.keys(state.accounts).length;

export const transferCount: Derivation<BankState, BankEvent, BankEffect, number> = ({ events }) =>
  events().filter((event) => event.type === 'funds-debited').length;

export const transferVolume: Derivation<BankState, BankEvent, BankEffect, number> = ({ events }) =>
  events()
    .filter((event): event is Extract<BankEvent, { type: 'funds-debited' }> =>
      event.type === 'funds-debited'
    )
    .reduce((sum, event) => sum + event.amount, 0);

export const averageTransfer: Derivation<BankState, BankEvent, BankEffect, number> = (context) => {
  const count = context.derive(transferCount);
  return count === 0 ? 0 : context.derive(transferVolume) / count;
};

export function createMockTransferClient() {
  const calls: BankEffect[] = [];
  return {
    calls,
    requestCount: () => calls.length,
    run: async (effect: BankEffect): Promise<void> => {
      calls.push(effect);
    },
  };
}
