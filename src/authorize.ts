import type {
  ActivityPubObject,
  ActivityPubValue,
  AuthorizationReason,
  AuthorizationResult,
  AuthorizeOptions,
  InboxActivity,
  ObjectFetcher,
  ObjectFetchResponse,
} from './types.ts';
import {
  actorFromKeyId,
  collectIds,
  idOf,
  parseHttpsUrl,
  sameHost,
} from './url.ts';

/** 只能作用于“自己拥有”的对象的活动类型。 */
const OWNED_ACTIVITIES = new Set(['Delete', 'Update']);

/** 携带内嵌对象、需要对象归属校验的活动类型。 */
const EMBEDDED_ACTIVITIES = new Set(['Create', 'Announce']);

function isObject(value: ActivityPubValue): value is ActivityPubObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function deny(
  activity: InboxActivity,
  reason: AuthorizationReason,
  detail?: string,
  actorId?: string,
): AuthorizationResult {
  return { allowed: false, reason, activity, actorId, detail };
}

function allow(
  activity: InboxActivity,
  actorId: string,
  fetchedObjectId?: string,
): AuthorizationResult {
  return {
    allowed: true,
    reason: 'ok',
    activity,
    actorId,
    fetchedObjectId,
  };
}

function attributedToMatchesActor(objectValue: ActivityPubValue, actorId: string): boolean {
  if (!isObject(objectValue)) {
    return false;
  }
  return collectIds(objectValue.attributedTo).includes(actorId);
}

/**
 * 通过注入的取回器获取一次对象。
 *
 * 安全约定：
 * - 调用方以 manual 模式发起请求，重定向响应通过 location 返回；
 * - 跨域重定向直接拒绝，同域重定向同样不跟随（每次授权只取回一次）；
 * - 任何失败都不会触发第二次请求。
 */
interface FetchFailure {
  reason: AuthorizationReason;
  detail: string;
}

type FetchResult = { ok: true; object: ActivityPubObject } | { ok: false; failure: FetchFailure };

async function fetchAuthoritativeObject(
  objectId: string,
  actorId: string,
  fetchObject: ObjectFetcher,
): Promise<FetchResult> {
  const targetUrl = parseHttpsUrl(objectId);

  let outcome: ActivityPubValue;
  try {
    outcome = await fetchObject(targetUrl.href);
  } catch (error) {
    return {
      ok: false,
      failure: {
        reason: 'fetch_failed',
        detail: `fetcher threw while retrieving ${targetUrl.href}: ${(error as Error).message}`,
      },
    };
  }

  if (!isObject(outcome)) {
    return {
      ok: false,
      failure: {
        reason: 'unsupported_response',
        detail: `response for ${targetUrl.href} is not an object`,
      },
    };
  }

  if (isFetchResponse(outcome)) {
    if (outcome.location !== undefined) {
      let redirectUrl: URL;
      try {
        redirectUrl = new URL(outcome.location, targetUrl.href);
      } catch {
        return {
          ok: false,
          failure: { reason: 'fetch_failed', detail: `unparseable redirect location: ${outcome.location}` },
        };
      }
      if (redirectUrl.hostname.toLowerCase() !== targetUrl.hostname.toLowerCase()) {
        return {
          ok: false,
          failure: {
            reason: 'cross_origin_redirect',
            detail: `refusing to follow redirect from ${targetUrl.href} to ${redirectUrl.href}`,
          },
        };
      }
      return {
        ok: false,
        failure: {
          reason: 'fetch_failed',
          detail: `redirects are not followed (single fetch policy): ${redirectUrl.href}`,
        },
      };
    }

    if (outcome.status < 200 || outcome.status >= 300) {
      return {
        ok: false,
        failure: { reason: 'fetch_failed', detail: `unexpected status ${outcome.status} for ${targetUrl.href}` },
      };
    }

    const body = outcome.body;
    if (!isObject(body)) {
      return {
        ok: false,
        failure: {
          reason: 'unsupported_response',
          detail: `response body for ${targetUrl.href} is not an object`,
        },
      };
    }
    return { ok: true, object: body };
  }

  return { ok: true, object: outcome };
}

function isFetchResponse(
  value: ActivityPubObject,
): value is ActivityPubObject & ObjectFetchResponse {
  // 仅当携带响应包装特征（status + body/location）时才视为响应，
  // 避免把恰好含有 status 字段的普通 ActivityPub 对象误判为响应。
  return (
    typeof value.status === 'number' &&
    ('body' in value || 'location' in value)
  );
}

/**
 * 收件箱 POST 授权入口。
 *
 * 校验顺序：
 * 1. HTTP 签名 keyId 推导出的 actor 必须与活动 actor 完全一致（防冒用 actor）；
 * 2. Delete/Update 仅当目标对象 attributedTo 等于该 actor 时生效；
 * 3. Create/Announce 内嵌对象与 actor 跨域时，从对象 id 重新取回，
 *    并要求权威副本 attributedTo 等于该 actor，否则忽略；
 * 4. 所有待取回的对象 id 必须是 https 且无 userinfo；
 * 5. 每次授权最多取回一次，且不跟随到其他域名。
 */
export async function authorizeInboxActivity(
  activity: InboxActivity,
  options: AuthorizeOptions,
): Promise<AuthorizationResult> {
  if (!isObject(activity)) {
    return deny({}, 'unsupported_response', 'activity must be a JSON object');
  }

  const activityType = typeof activity.type === 'string' ? activity.type : undefined;
  if (activityType === undefined) {
    return deny(activity, 'missing_activity_type', 'activity has no string type');
  }

  // 1. 签名 actor 必须等于活动 actor。
  let signerActorId: string;
  try {
    signerActorId = actorFromKeyId(options.signature.keyId);
  } catch (error) {
    return deny(activity, 'invalid_object_id', `invalid keyId: ${(error as Error).message}`);
  }

  const activityActorId = idOf(activity.actor as string | ActivityPubObject);
  if (activityActorId === undefined) {
    return deny(activity, 'invalid_activity_actor', 'activity actor is missing or has no id', signerActorId);
  }
  let actorUrl: URL;
  try {
    actorUrl = parseHttpsUrl(activityActorId);
  } catch (error) {
    return deny(
      activity,
      'invalid_activity_actor',
      `activity actor id rejected: ${(error as Error).message}`,
      signerActorId,
    );
  }

  if (actorUrl.href !== signerActorId) {
    return deny(
      activity,
      'signer_actor_mismatch',
      `signer ${signerActorId} is not activity actor ${actorUrl.href}`,
      signerActorId,
    );
  }

  // 2. Delete/Update：只能操作 attributedTo 为自己的对象。
  if (OWNED_ACTIVITIES.has(activityType)) {
    return authorizeOwnedActivity(activity, activityType, signerActorId, options);
  }

  // 3. Create/Announce：跨域内嵌对象需重新取回并核对归属。
  if (EMBEDDED_ACTIVITIES.has(activityType)) {
    return authorizeEmbeddedActivity(activity, activityType, signerActorId, options);
  }

  // 其他活动类型在签名 actor 一致后放行（本包不定义其语义）。
  return allow(activity, signerActorId);
}

async function authorizeOwnedActivity(
  activity: InboxActivity,
  activityType: string,
  actorId: string,
  options: AuthorizeOptions,
): Promise<AuthorizationResult> {
  const target = activity.object;

  // 内嵌对象：直接核对 attributedTo；内嵌对象携带 id 时同样要求 id 合法。
  if (isObject(target)) {
    if (typeof target.id === 'string') {
      try {
        parseHttpsUrl(target.id);
      } catch (error) {
        return deny(activity, 'invalid_object_id',
          `${activityType} object id rejected: ${(error as Error).message}`, actorId);
      }
    }
    if (!attributedToMatchesActor(target, actorId)) {
      return deny(activity, 'attribution_mismatch',
        `${activityType} target is not attributed to ${actorId}`, actorId);
    }
    return allow(activity, actorId);
  }

  // 裸 id（Tombstone 等常见形式）：取回一次再核对归属。
  if (typeof target === 'string') {
    let targetUrl: URL;
    try {
      targetUrl = parseHttpsUrl(target);
    } catch (error) {
      return deny(activity, 'invalid_object_id',
        `${activityType} object id rejected: ${(error as Error).message}`, actorId);
    }

    const fetched = await fetchAuthoritativeObject(targetUrl.href, actorId, options.fetchObject);
    if (!fetched.ok) {
      return deny(activity, fetched.failure.reason, fetched.failure.detail, actorId);
    }
    if (!attributedToMatchesActor(fetched.object, actorId)) {
      return deny(activity, 'attribution_mismatch',
        `${activityType} target ${targetUrl.href} is not attributed to ${actorId}`, actorId);
    }
    return { ...allow(activity, actorId), fetchedObjectId: targetUrl.href };
  }

  return deny(activity, 'object_not_found',
    `${activityType} activity has no resolvable object`, actorId);
}

async function authorizeEmbeddedActivity(
  activity: InboxActivity,
  activityType: string,
  actorId: string,
  options: AuthorizeOptions,
): Promise<AuthorizationResult> {
  const embedded = activity.object;

  if (typeof embedded === 'string') {
    // 裸对象 id：无论是否同域都取回一次，再核对归属。
    let objectUrl: URL;
    try {
      objectUrl = parseHttpsUrl(embedded);
    } catch (error) {
      return deny(activity, 'invalid_object_id',
        `${activityType} object id rejected: ${(error as Error).message}`, actorId);
    }

    const fetched = await fetchAuthoritativeObject(objectUrl.href, actorId, options.fetchObject);
    if (!fetched.ok) {
      return deny(activity, fetched.failure.reason, fetched.failure.detail, actorId);
    }
    if (!attributedToMatchesActor(fetched.object, actorId)) {
      return deny(activity, 'attribution_mismatch',
        `fetched object ${objectUrl.href} is not attributed to ${actorId}`, actorId);
    }

    const normalized: InboxActivity = { ...activity, object: fetched.object };
    return { ...allow(normalized, actorId), fetchedObjectId: objectUrl.href };
  }

  if (!isObject(embedded)) {
    return deny(activity, 'object_not_found',
      `${activityType} activity has no resolvable object`, actorId);
  }

  const embeddedId = embedded.id;
  if (typeof embeddedId !== 'string') {
    return deny(activity, 'invalid_object_id',
      `${activityType} embedded object has no string id`, actorId);
  }

  let objectUrl: URL;
  try {
    objectUrl = parseHttpsUrl(embeddedId);
  } catch (error) {
    return deny(activity, 'invalid_object_id',
      `${activityType} object id rejected: ${(error as Error).message}`, actorId);
  }

  // 同域：信任投递中的内嵌对象，不发起取回。
  if (sameHost(objectUrl.href, actorId)) {
    if (!attributedToMatchesActor(embedded, actorId)) {
      return deny(activity, 'attribution_mismatch',
        `${activityType} embedded object is not attributed to ${actorId}`, actorId);
    }
    return allow(activity, actorId);
  }

  // 跨域：丢弃内嵌副本，从对象 id 重新取回权威副本，且只取回一次。
  const fetched = await fetchAuthoritativeObject(objectUrl.href, actorId, options.fetchObject);
  if (!fetched.ok) {
    return deny(activity, fetched.failure.reason, fetched.failure.detail, actorId);
  }
  if (!attributedToMatchesActor(fetched.object, actorId)) {
    return deny(activity, 'attribution_mismatch',
      `authoritative object ${objectUrl.href} is not attributed to ${actorId}`, actorId);
  }

  const normalized: InboxActivity = { ...activity, object: fetched.object };
  return { ...allow(normalized, actorId), fetchedObjectId: objectUrl.href };
}
