export interface DocumentState {
  scheme: string;
  registrableDomain: string;
  path: string;
}

export interface IdleState {
  phase: 'idle';
  name: string;
}

export interface ActiveState {
  phase: 'active';
  document: DocumentState;
  name: string;
}

export type GuardState = IdleState | ActiveState;

export type PipelineEvent =
  | { type: 'document-committed'; document: DocumentState }
  | { type: 'name-set'; value: string };

const initialState: GuardState = { phase: 'idle', name: '' };

function normalizeScheme(scheme: string): string {
  return scheme.toLowerCase();
}

function normalizeDomain(domain: string): string {
  return domain.toLowerCase().replace(/\.$/, '');
}

function normalizeDocument(document: DocumentState): DocumentState {
  return {
    scheme: normalizeScheme(document.scheme),
    registrableDomain: normalizeDomain(document.registrableDomain),
    path: document.path || '/',
  };
}

export function isSameBrowsingSite(a: DocumentState, b: DocumentState): boolean {
  return (
    normalizeScheme(a.scheme) === normalizeScheme(b.scheme) &&
    normalizeDomain(a.registrableDomain) === normalizeDomain(b.registrableDomain)
  );
}

export function transition(state: GuardState, event: PipelineEvent): GuardState {
  switch (event.type) {
    case 'document-committed': {
      const document = normalizeDocument(event.document);
      const name =
        state.phase === 'active' && isSameBrowsingSite(state.document, document)
          ? state.name
          : '';
      return { phase: 'active', document, name };
    }
    case 'name-set': {
      if (state.phase !== 'active') {
        return state;
      }
      return { ...state, name: String(event.value) };
    }
  }
}

export class WindowNameGuard {
  private state: GuardState = initialState;

  dispatch(event: PipelineEvent): GuardState {
    this.state = transition(this.state, event);
    return this.state;
  }

  get snapshot(): GuardState {
    return this.state;
  }

  get windowName(): string {
    return this.state.name;
  }

  get currentDocument(): DocumentState | null {
    return this.state.phase === 'active' ? this.state.document : null;
  }
}

export function createWindowNameGuard(): WindowNameGuard {
  return new WindowNameGuard();
}
