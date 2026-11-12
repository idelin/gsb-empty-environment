import type {
  ActivationToken,
  AuditEntry,
  AuditReason,
  ClipboardMode,
  ClipboardPermissionRequest,
  PermissionGrant,
  Permissioner,
  SystemClipboard,
} from './types.ts';
import type { BrowserEnvironment } from './environment.ts';
import type { ActivationTracker } from './activation.ts';
import type { Clock } from './clock.ts';
import {
  ClipboardAbortError,
  ExpiredActivationError,
  NoTransientActivationError,
  PermissionDeniedError,
  StaleDocumentGenerationError,
  StaleFocusGenerationError,
  TabNotFoundError,
} from './errors.ts';

export interface ClipboardGuardDeps {
  readonly environment: BrowserEnvironment;
  readonly activations: ActivationTracker;
  readonly permissioner: Permissioner;
  readonly clipboard: SystemClipboard;
  readonly clock: Clock;
}

let requestSequence = 0;

/**
 * 剪贴板许可守卫。
 *
 * 绑定模型：
 * 1. 动作必须由仍在有效期内的用户瞬态激活发起，且激活与标签页绑定；
 * 2. 许可请求携带发起时刻的文档代与全局焦点代快照；
 * 3. 许可回调到达时，在任何剪贴板 I/O 之前重新复核三项绑定，
 *    任一失配即拒绝；
 * 4. 授权按请求独立发放且单次消费，一个请求失败不会触碰另一个请求的授权。
 */
export class ClipboardGuard {
  readonly auditLog: AuditEntry[] = [];

  private readonly deps: ClipboardGuardDeps;

  constructor(deps: ClipboardGuardDeps) {
    this.deps = deps;
  }

  copy(tabId: string, text: string, signal?: AbortSignal): Promise<void> {
    return this.execute('write', tabId, signal, (context) => {
      this.deps.clipboard.write(text, context);
    });
  }

  paste(tabId: string, signal?: AbortSignal): Promise<string> {
    let result = '';
    return this.execute('read', tabId, signal, (context) => {
      result = this.deps.clipboard.read(context);
    }).then(() => result);
  }

  private async execute(
    mode: ClipboardMode,
    tabId: string,
    signal: AbortSignal | undefined,
    operation: (context: {
      grantId: string;
      requestId: string;
      mode: ClipboardMode;
      tabId: string;
    }) => void,
  ): Promise<void> {
    const requestId = `req-${(requestSequence += 1)}`;

    if (signal?.aborted) {
      throw new ClipboardAbortError(requestId);
    }

    if (!this.deps.environment.hasTab(tabId)) {
      this.audit('tab-missing', requestId, mode, tabId, {});
      throw new TabNotFoundError(
        tabId,
        { tabId, documentGeneration: -1, focusGeneration: -1 },
      );
    }

    const activation = this.requireLiveActivation(mode, tabId, requestId);
    const snapshot = this.deps.environment.snapshot(tabId);

    const request: ClipboardPermissionRequest = {
      requestId,
      mode,
      snapshot,
      activationTokenId: activation.tokenId,
    };

    this.audit('request-created', requestId, mode, tabId, {
      activationTokenId: activation.tokenId,
      documentGeneration: snapshot.documentGeneration,
      focusGeneration: snapshot.focusGeneration,
    });

    if (signal?.aborted) {
      this.audit('aborted-while-waiting', requestId, mode, tabId, {});
      throw new ClipboardAbortError(requestId);
    }

    let grant: PermissionGrant;
    try {
      grant = await this.deps.permissioner.requestPermission(request);
    } catch (error) {
      const causeMessage = error instanceof Error ? error.message : String(error);
      this.audit('permission-denied', requestId, mode, tabId, { causeMessage });
      throw new PermissionDeniedError(mode, requestId, causeMessage);
    }

    if (signal?.aborted) {
      this.deps.permissioner.invalidateGrant(grant, 'aborted-after-grant');
      this.audit('grant-abandoned-after-abort', requestId, mode, tabId, {
        grantId: grant.grantId,
      });
      throw new ClipboardAbortError(requestId);
    }

    try {
      this.recheckAllBindings(requestId, mode, grant, activation.tokenId);

      if (this.deps.permissioner.isConsumed(grant.grantId)) {
        this.audit('recheck-failed', requestId, mode, tabId, {
          grantId: grant.grantId,
          reason: 'grant-already-consumed',
        });
        throw new PermissionDeniedError(
          mode,
          requestId,
          `grant ${grant.grantId} was already consumed`,
        );
      }
    } catch (error) {
      this.deps.permissioner.invalidateGrant(grant, 'recheck-failed');
      throw error;
    }

    this.deps.permissioner.markConsumed(grant);
    this.audit('grant-consumed', requestId, mode, tabId, {
      grantId: grant.grantId,
    });

    const context = {
      grantId: grant.grantId,
      requestId,
      mode,
      tabId,
    };
    try {
      operation(context);
    } catch (error) {
      this.deps.permissioner.invalidateGrant(
        grant,
        error instanceof Error ? error.message : String(error),
      );
      throw error;
    }
    this.audit('clipboard-operation', requestId, mode, tabId, {
      grantId: grant.grantId,
    });
  }

  private requireLiveActivation(
    mode: ClipboardMode,
    tabId: string,
    requestId: string,
  ): ActivationToken {
    const now = this.deps.clock.now();
    const token = this.deps.activations.latestForTab(tabId);
    if (!token || now >= token.expiresAt) {
      this.audit('no-activation', requestId, mode, tabId, {});
      throw new NoTransientActivationError(mode, tabId);
    }
    return token;
  }

  /** 回调到达时的全量复核：标签页、文档代、焦点代、激活，任一失配即拒绝。 */
  private recheckAllBindings(
    requestId: string,
    mode: ClipboardMode,
    grant: PermissionGrant,
    activationTokenId: string,
  ): void {
    const { tabId } = grant.snapshot;
    const current = this.deps.environment.getCurrentSnapshot(tabId);

    if (!current) {
      this.audit('recheck-failed', requestId, mode, tabId, {
        grantId: grant.grantId,
        reason: 'tab-closed',
      });
      throw new TabNotFoundError(tabId, grant.snapshot);
    }

    if (current.documentGeneration !== grant.snapshot.documentGeneration) {
      this.audit('recheck-failed', requestId, mode, tabId, {
        grantId: grant.grantId,
        reason: 'document-generation',
        expected: grant.snapshot.documentGeneration,
        actual: current.documentGeneration,
      });
      throw new StaleDocumentGenerationError(
        grant.snapshot.documentGeneration,
        current.documentGeneration,
        tabId,
      );
    }

    if (current.focusGeneration !== grant.snapshot.focusGeneration) {
      this.audit('recheck-failed', requestId, mode, tabId, {
        grantId: grant.grantId,
        reason: 'focus-generation',
        expected: grant.snapshot.focusGeneration,
        actual: current.focusGeneration,
      });
      throw new StaleFocusGenerationError(
        grant.snapshot.focusGeneration,
        current.focusGeneration,
        tabId,
      );
    }

    const liveToken =
      this.deps.activations.isCurrentActivation(activationTokenId);
    if (!liveToken) {
      const original = this.deps.activations.get(activationTokenId);
      const expiredAt = original?.expiresAt ?? -1;
      this.audit('recheck-failed', requestId, mode, tabId, {
        grantId: grant.grantId,
        reason: 'expired-activation',
        activationTokenId,
      });
      throw new ExpiredActivationError(
        activationTokenId,
        expiredAt,
        this.deps.clock.now(),
      );
    }
  }

  private audit(
    reason: AuditReason,
    requestId: string,
    mode: ClipboardMode,
    tabId: string,
    detail: Readonly<Record<string, string | number | boolean>>,
  ): void {
    this.auditLog.push({
      at: this.deps.clock.now(),
      requestId,
      reason,
      mode,
      tabId,
      detail,
    });
  }
}
