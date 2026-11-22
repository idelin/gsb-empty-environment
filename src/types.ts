/**
 * Structural browser environment types.
 *
 * The mediator never touches global DOM objects directly; it talks to this
 * narrow surface so page code can run the real browser while tests can inject
 * fakes that emulate cross-site frames and denied permissions policies.
 */

export interface AncestorOriginList {
  readonly length: number;
  item(index: number): string | null;
  contains(origin: string): boolean;
  readonly [index: number]: string;
}

export interface LocationLike {
  readonly origin: string;
  readonly hostname: string;
  readonly ancestorOrigins?: AncestorOriginList;
}

export interface WindowLike {
  self: WindowLike;
  top: WindowLike;
  parent: WindowLike;
  readonly location: LocationLike;
  addEventListener?(type: string, listener: () => void): void;
  removeEventListener?(type: string, listener: () => void): void;
}

export interface PermissionsPolicyLike {
  allowsFeature(feature: string): boolean;
}

export interface DocumentLike {
  readonly location: LocationLike;
  readonly permissionsPolicy?: PermissionsPolicyLike;
  readonly featurePolicy?: PermissionsPolicyLike;
}

export type PermissionStateLike = 'granted' | 'denied' | 'prompt';

export interface PermissionsLike {
  query(descriptor: { name: string }): Promise<{ state: PermissionStateLike }>;
}

export interface PublicKeyCredentialDescriptorLike {
  readonly id: ArrayBuffer | ArrayBufferView;
  readonly type?: string;
  readonly transports?: string[];
}

export interface PublicKeyCredentialRequestOptionsLike {
  readonly challenge: ArrayBuffer | ArrayBufferView;
  readonly rpId?: string;
  readonly allowCredentials?: PublicKeyCredentialDescriptorLike[];
  readonly timeout?: number;
  readonly userVerification?: string;
  readonly extensions?: Record<string, unknown>;
}

export interface PublicKeyCredentialLike {
  readonly id: string;
  readonly type: 'public-key';
  readonly rawId: ArrayBuffer;
}

export interface CredentialGetRequest {
  readonly mediation: 'conditional';
  readonly publicKey: PublicKeyCredentialRequestOptionsLike;
  readonly signal: AbortSignal;
}

export interface CredentialsContainerLike {
  get(request: CredentialGetRequest): Promise<PublicKeyCredentialLike>;
}

export interface NavigatorLike {
  readonly credentials?: CredentialsContainerLike;
  readonly permissions?: PermissionsLike;
}

export interface AbortControllerConstructor {
  new (): AbortController;
}

export interface BrowserEnvironment {
  readonly window: WindowLike;
  readonly document: DocumentLike;
  readonly navigator: NavigatorLike;
  readonly AbortController: AbortControllerConstructor;
}

export interface ConditionalGetInput {
  readonly publicKey: PublicKeyCredentialRequestOptionsLike;
  readonly signal?: AbortSignal;
}
