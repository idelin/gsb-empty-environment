export type FedCMEndpoint = "accounts" | "id_assertion" | "client_metadata";

/**
 * An account as returned by an accounts endpoint.
 *
 * `id` is the FedCM account identifier. `account_id` is accepted as an alias
 * because some identity providers keep that spelling in their session store.
 */
export interface FedCMAccount {
  id?: string | number;
  account_id?: string | number;
  email?: string;
  email_verified?: boolean;
  name?: string;
  picture?: string;
  [claim: string]: unknown;
}

/** The accounts belonging to the server-side session selected by Cookie. */
export interface FedCMSession {
  accounts: readonly FedCMAccount[];
  [key: string]: unknown;
}

/** The subset of client metadata that matters for FedCM origin validation. */
export interface ClientMetadata {
  client_id: string;
  origins: readonly string[];
  [key: string]: unknown;
}

export type ClientMetadataSource =
  | ClientMetadata
  | readonly ClientMetadata[]
  | ReadonlyMap<string, ClientMetadata>
  | ((clientId: string) => ClientMetadata | null | undefined);

export type SessionLookup = (
  cookieHeader: string | null,
) => FedCMSession | null | undefined;

export type SessionSource = FedCMSession | SessionLookup;
