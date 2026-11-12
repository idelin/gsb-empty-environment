import { type ScopeMatch, normalizeUrl } from './url.ts';
import { fail } from './errors.ts';

export type VersionState = 'installing' | 'waiting' | 'active' | 'redundant';

export interface ClientSnapshot {
  readonly id: string;
  readonly url: string;
  readonly controllerId: string | null;
}

export interface InstallHandle {
  readonly versionId: string;
  complete(): ServiceWorkerVersion;
  fail(): ServiceWorkerVersion;
}

export class ServiceWorkerVersion {
  readonly id: string;
  readonly scope: string;
  readonly scriptUrl: string;
  state: VersionState;
  readonly skipWaiting: boolean;

  constructor(
    id: string,
    scope: string,
    scriptUrl: string,
    state: VersionState,
    skipWaiting: boolean,
  ) {
    this.id = id;
    this.scope = scope;
    this.scriptUrl = scriptUrl;
    this.state = state;
    this.skipWaiting = skipWaiting;
  }
}

interface ClientRecord {
  id: string;
  url: string;
  controllerId: string | null;
}

interface RegistrationRecord {
  scopeMatch: ScopeMatch;
  origin: string;
  installing: ServiceWorkerVersion | null;
  waiting: ServiceWorkerVersion | null;
  active: ServiceWorkerVersion | null;
}

export interface ActivateResult {
  readonly activatedVersionId: string;
  readonly replacedVersionId: string | null;
  readonly state: VersionState;
}

export class RegistrationModel {
  private readonly registrations = new Map<string, RegistrationRecord>();
  private readonly versions = new Map<string, ServiceWorkerVersion>();
  private readonly clients = new Map<string, ClientRecord>();
  private counter = 0;

  startInstall(input: {
    scope: string;
    origin: string;
    scriptUrl: string;
    skipWaiting?: boolean;
  }): InstallHandle {
    const scopeUrl = normalizeUrl(new URL(input.scope, input.origin).href);
    const scopeMatch: ScopeMatch = {
      scopePath: input.scope,
      scopeSegments: scopeUrl.segments,
    };

    let record = this.registrations.get(input.scope);
    if (record === undefined) {
      record = {
        scopeMatch,
        origin: input.origin,
        installing: null,
        waiting: null,
        active: null,
      };
      this.registrations.set(input.scope, record);
    }

    const previousInstalling = record.installing;
    const previousWaiting = record.waiting;
    if (previousInstalling !== null) {
      previousInstalling.state = 'redundant';
    }
    if (previousWaiting !== null) {
      previousWaiting.state = 'redundant';
    }

    const version = new ServiceWorkerVersion(
      `v${++this.counter}`,
      input.scope,
      input.scriptUrl,
      'installing',
      input.skipWaiting ?? false,
    );
    record.installing = version;
    record.waiting = null;
    this.versions.set(version.id, version);

    return {
      versionId: version.id,
      complete: () => this.completeInstall(version.id),
      fail: () => this.failInstall(version.id),
    };
  }

  private recordFor(versionId: string): RegistrationRecord {
    for (const record of this.registrations.values()) {
      if (
        record.installing?.id === versionId ||
        record.waiting?.id === versionId ||
        record.active?.id === versionId
      ) {
        return record;
      }
    }
    fail('BAD_STATE', '版本不属于任何注册项。', { versionId });
  }

  completeInstall(versionId: string): ServiceWorkerVersion {
    const version = this.requireVersion(versionId);
    if (version.state === 'redundant') {
      fail('BAD_STATE', '迟到的安装完成被忽略：该版本已被标记为 redundant。', {
        versionId,
      });
    }
    if (version.state !== 'installing') {
      fail('BAD_STATE', '只有 installing 状态的版本才能完成安装。', {
        versionId,
        state: version.state,
      });
    }

    const record = this.recordFor(versionId);
    record.installing = null;
    if (record.active === null) {
      record.active = version;
      version.state = 'active';
    } else {
      record.waiting = version;
      version.state = 'waiting';
    }
    return version;
  }

  failInstall(versionId: string): ServiceWorkerVersion {
    const version = this.requireVersion(versionId);
    if (version.state !== 'installing') {
      fail('BAD_STATE', '只有 installing 状态的版本才能失败。', {
        versionId,
        state: version.state,
      });
    }
    const record = this.recordFor(versionId);
    record.installing = null;
    version.state = 'redundant';
    return version;
  }

  canActivate(versionId: string): boolean {
    const version = this.requireVersion(versionId);
    if (version.state !== 'waiting') {
      return false;
    }
    const record = this.recordFor(versionId);
    if (version.skipWaiting) {
      return true;
    }
    for (const client of this.clients.values()) {
      if (client.controllerId === record.active?.id) {
        return false;
      }
    }
    return true;
  }

  activate(versionId: string, options?: { force?: boolean }): ActivateResult {
    const version = this.requireVersion(versionId);
    if (version.state === 'redundant') {
      fail('BAD_STATE', 'redundant 版本不能激活。', { versionId });
    }
    if (version.state === 'active') {
      return {
        activatedVersionId: version.id,
        replacedVersionId: null,
        state: 'active',
      };
    }
    if (version.state !== 'waiting') {
      fail('BAD_STATE', '只有 waiting 版本可以请求激活。', {
        versionId,
        state: version.state,
      });
    }

    if (options?.force !== true && !this.canActivate(versionId)) {
      fail('BAD_STATE', '仍有旧版本控制的客户，新版本必须等待，不能抢占旧客户。', {
        versionId,
      });
    }

    const record = this.recordFor(versionId);
    const replaced = record.active;
    if (replaced !== null && replaced.id !== version.id) {
      replaced.state = 'redundant';
    }
    record.active = version;
    record.waiting = null;
    version.state = 'active';

    return {
      activatedVersionId: version.id,
      replacedVersionId: replaced?.id ?? null,
      state: 'active',
    };
  }

  openClient(id: string, url: string): ClientSnapshot {
    const record = this.findRegistrationForUrl(url);
    const client: ClientRecord = {
      id,
      url,
      controllerId: record?.active?.id ?? null,
    };
    this.clients.set(id, client);
    return this.snapshot(client);
  }

  navigateClient(id: string, url: string): ClientSnapshot {
    const client = this.requireClient(id);
    const record = this.findRegistrationForUrl(url);
    client.url = url;
    client.controllerId = record?.active?.id ?? null;
    return this.snapshot(client);
  }

  unloadClient(id: string): void {
    this.clients.delete(id);
  }

  claim(versionId: string): readonly string[] {
    const version = this.requireVersion(versionId);
    if (version.state !== 'active') {
      fail('BAD_STATE', '只有 active 版本才能 claim 客户。', {
        versionId,
        state: version.state,
      });
    }
    const claimed: string[] = [];
    for (const client of this.clients.values()) {
      const record = this.findRegistrationForUrl(client.url);
      if (record?.active?.id === version.id && client.controllerId !== version.id) {
        client.controllerId = version.id;
        claimed.push(client.id);
      }
    }
    return claimed;
  }

  getClient(id: string): ClientSnapshot {
    return this.snapshot(this.requireClient(id));
  }

  getVersion(versionId: string): ServiceWorkerVersion {
    return this.requireVersion(versionId);
  }

  private findRegistrationForUrl(url: string): RegistrationRecord | null {
    const target = normalizeUrl(url);
    let best: RegistrationRecord | null = null;
    for (const record of this.registrations.values()) {
      if (record.origin !== target.origin) {
        continue;
      }
      const prefix = record.scopeMatch.scopeSegments;
      if (
        prefix.length <= target.segments.length &&
        prefix.every((segment, index) => target.segments[index] === segment) &&
        (best === null || prefix.length > best.scopeMatch.scopeSegments.length)
      ) {
        best = record;
      }
    }
    return best;
  }

  private requireVersion(versionId: string): ServiceWorkerVersion {
    const version = this.versions.get(versionId);
    if (version === undefined) {
      fail('BAD_STATE', '未知版本。', { versionId });
    }
    return version;
  }

  private requireClient(id: string): ClientRecord {
    const client = this.clients.get(id);
    if (client === undefined) {
      fail('BAD_STATE', '未知客户。', { clientId: id });
    }
    return client;
  }

  private snapshot(client: ClientRecord): ClientSnapshot {
    return {
      id: client.id,
      url: client.url,
      controllerId: client.controllerId,
    };
  }
}
