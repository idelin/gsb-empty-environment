import type { Migration, State } from './store.ts';

/** v1 -> v2: rename user.name -> user.fullName */
export const v1_to_v2: Migration = {
  from: 1,
  to: 2,
  description: 'rename user.name to user.fullName',
  renames: { 'user.name': 'user.fullName' },
  migrate(state: State): State {
    const next = structuredClone(state);
    const user = next.user as Record<string, unknown>;
    user.fullName = user.name;
    delete user.name;
    return next;
  },
};

/** v2 -> v3: introduce settings.theme with a default */
export const v2_to_v3: Migration = {
  from: 2,
  to: 3,
  description: 'add settings.theme default',
  migrate(state: State): State {
    const next = structuredClone(state);
    next.settings = { theme: 'light' };
    return next;
  },
};

/** v3 -> v4: deliberately broken migration to demonstrate rollback */
export const v3_to_v4_broken: Migration = {
  from: 3,
  to: 4,
  description: 'broken migration (demo of rollback)',
  migrate(): State {
    throw new Error('simulated migration failure');
  },
};
