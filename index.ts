/**
 * Topics API 浏览器宿主端实现（document.browsingTopics() 的宿主侧逻辑）。
 *
 * 安全属性：
 * - 每个每周纪元独立保存 (话题 -> 观察过该话题的调用方集合)。
 * - 调用方只能拿到自己在该纪元观察过的话题；从未观察过的话题不会泄露。
 * - 只从最近 3 个纪元各取至多一个话题，因此最多返回 3 个。
 * - 每个 (顶层站点, 调用方, 纪元) 的取舍/选话题由宿主种子经 HMAC 确定性派生，
 *   同一组合重复调用结果完全相同。
 * - 每纪元使用独立随机种子（独立 HMAC 密钥），不同纪元的随机结果互不可关联。
 * - 跨源 iframe 若没有 browsing-topics 权限策略，一律返回空数组。
 */

/** 真实 API 中的话题 id 是 0..taxonomySize-1 的整数。 */
export type TopicId = number;

/** document.browsingTopics() 解析出的单个话题（形状对齐浏览器 API）。 */
export interface BrowsingTopic {
  topic: TopicId;
  version: string;
  configVersion: string;
  modelVersion: string;
  taxonomyVersion: string;
}

/** 一次 document.browsingTopics() 调用所处的浏览上下文。 */
export interface BrowsingContext {
  /** 当前文档所在的顶层站点（eTLD+1）。 */
  topLevelSite: string;
  /** 发起调用的脚本所属调用方站点（eTLD+1）。 */
  caller: string;
  /** 调用是否发生在 iframe 中。 */
  inIframe: boolean;
  /** iframe 是否与顶层文档跨源；顶层文档调用时为 false。 */
  isCrossOrigin: boolean;
  /** 浏览上下文是否被授予 browsing-topics 权限策略。 */
  hasBrowsingTopicsPolicy: boolean;
}

export interface TopicsHostOptions {
  /** 话题分类版本，原样回填到返回对象中。 */
  taxonomyVersion?: string;
  /** 模型版本，原样回填到返回对象中。 */
  modelVersion?: string;
  /**
   * 每个 (顶层站点, 调用方, 纪元) 组合被整体过滤掉（该纪元不贡献话题）的概率，
   * 对应规范里每用户每纪元约 5% 的过滤噪声。默认 0，保证测试确定；
   * 该取舍同样由宿主种子确定性决定，重复调用稳定。
   */
  filterRate?: number;
  /** 保留多少个最近纪元参与返回，规范固定为 3。 */
  epochsToReturn?: number;
}

/** 单个纪元的持久状态：独立随机种子 + 话题观察集合。 */
interface EpochState {
  epoch: number;
  /** 纪元独立种子；HMAC 密钥材料，保证跨纪元随机不可关联。 */
  seed: Uint8Array;
  /** topicId -> 观察过该话题的调用方站点集合。 */
  observations: Map<TopicId, Set<string>>;
}

/** 测试/宿主内部使用：标注话题来自哪个纪元。 */
export interface EpochTopic extends BrowsingTopic {
  epoch: number;
}

const HMAC_ALG = { name: "HMAC", hash: "SHA-256" } as const;
const SEED_BYTES = 32;

export class TopicsHost {
  private readonly taxonomyVersion: string;
  private readonly modelVersion: string;
  private readonly filterRate: number;
  private readonly epochsToReturn: number;
  private readonly epochs: EpochState[] = [];
  private nextEpoch = 0;
  private readonly hmacKeyCache = new Map<number, Promise<CryptoKey>>();

  constructor(options: TopicsHostOptions = {}) {
    const filterRate = options.filterRate ?? 0;
    if (filterRate < 0 || filterRate > 1) {
      throw new RangeError("filterRate 必须位于 [0, 1]");
    }
    this.filterRate = filterRate;
    this.epochsToReturn = options.epochsToReturn ?? 3;
    this.taxonomyVersion = options.taxonomyVersion ?? "chrome.1";
    this.modelVersion = options.modelVersion ?? "1";
  }

  /**
   * 开启一个新的每周纪元。生产宿主不传 seed，由 crypto 随机生成；
   * 测试可注入固定 seed 以复现结果。
   */
  async startEpoch(seed?: Uint8Array): Promise<number> {
    const epoch = this.nextEpoch++;
    const epochSeed = seed
      ? Uint8Array.from(seed)
      : crypto.getRandomValues(new Uint8Array(SEED_BYTES));
    if (epochSeed.length !== SEED_BYTES) {
      throw new RangeError(`纪元种子必须为 ${SEED_BYTES} 字节`);
    }
    this.epochs.push({ epoch, seed: epochSeed, observations: new Map() });
    return epoch;
  }

  /**
   * 记录一次观察：调用方 caller 在顶层站点 topLevelSite 上接触到话题 topic。
   * 缺省记入当前（最新）纪元。
   */
  observe(topic: TopicId, caller: string, topLevelSite?: string): void {
    if (!Number.isInteger(topic) || topic < 0) {
      throw new RangeError("话题 id 必须是非负整数");
    }
    const epochState = this.latestEpoch();
    if (!epochState) {
      throw new Error("尚未开启任何纪元");
    }
    let observers = epochState.observations.get(topic);
    if (!observers) {
      observers = new Set();
      epochState.observations.set(topic, observers);
    }
    observers.add(caller);
    // 顶层站点参数保留在签名中以贴合宿主观察入口；过滤基于调用方而非顶层站点。
    void topLevelSite;
  }

  /** document.browsingTopics() 的宿主实现。 */
  async browsingTopics(context: BrowsingContext): Promise<BrowsingTopic[]> {
    const withEpoch = await this.browsingTopicsWithEpoch(context);
    return withEpoch.map(
      ({ topic, version, configVersion, modelVersion, taxonomyVersion }) => ({
        topic,
        version,
        configVersion,
        modelVersion,
        taxonomyVersion,
      }),
    );
  }

  /** 带纪元标注的变体，供宿主内部与测试验证“每纪元一个话题”。 */
  async browsingTopicsWithEpoch(context: BrowsingContext): Promise<EpochTopic[]> {
    if (!this.permissionAllowed(context)) {
      return [];
    }
    const recent = [...this.epochs]
      .sort((a, b) => b.epoch - a.epoch)
      .slice(0, this.epochsToReturn)
      .sort((a, b) => a.epoch - b.epoch);

    const results: EpochTopic[] = [];
    for (const epochState of recent) {
      const picked = await this.pickTopicForEpoch(epochState, context);
      if (picked !== undefined) {
        results.push({
          topic: picked,
          epoch: epochState.epoch,
          version: this.taxonomyVersion,
          configVersion: this.taxonomyVersion,
          modelVersion: this.modelVersion,
          taxonomyVersion: this.taxonomyVersion,
        });
      }
    }
    return results;
  }

  /**
   * 权限策略：只有跨源 iframe 且未被授予 browsing-topics 策略时拒绝。
   * 顶层文档与同源 iframe 默认可用。
   */
  private permissionAllowed(context: BrowsingContext): boolean {
    if (context.inIframe && context.isCrossOrigin && !context.hasBrowsingTopicsPolicy) {
      return false;
    }
    return true;
  }

  private latestEpoch(): EpochState | undefined {
    return this.epochs[this.epochs.length - 1];
  }

  /**
   * 为某 (顶层站点, 调用方, 纪元) 组合确定性地决定该纪元贡献哪个话题。
   * 随机源是该纪元独立种子派生的 HMAC，因此：
   * - 同组合重复调用 -> 同一 HMAC 输出 -> 结果一致；
   * - 跨纪元 -> 不同 HMAC 密钥 -> 输出不可关联；
   * - 调用方未观察过任何话题 -> 该纪元不贡献话题（隐私过滤）。
   */
  private async pickTopicForEpoch(
    epochState: EpochState,
    context: BrowsingContext,
  ): Promise<TopicId | undefined> {
    const observed = this.observedTopicsFor(epochState, context.caller);
    if (observed.length === 0) {
      return undefined;
    }

    const draw = await this.comboDraw(epochState, context.topLevelSite, context.caller);

    // 确定性的每组合过滤取舍（噪声开关），重复调用稳定。
    if (draw.keepRoll < this.filterRate) {
      return undefined;
    }
    return observed[Number(draw.topicIndex % BigInt(observed.length))];
  }

  /** 调用方在该纪元观察过的话题，按 id 规范化排序以保证选择确定。 */
  private observedTopicsFor(epochState: EpochState, caller: string): TopicId[] {
    const topics: TopicId[] = [];
    for (const [topic, observers] of epochState.observations) {
      if (observers.has(caller)) {
        topics.push(topic);
      }
    }
    return topics.sort((a, b) => a - b);
  }

  /**
   * 由 (纪元种子, 顶层站点, 调用方) 派生两个伪随机值：
   * keepRoll ∈ [0,1) 用于过滤取舍，topicIndex 为均匀 u64 用于选话题。
   */
  private async comboDraw(
    epochState: EpochState,
    topLevelSite: string,
    caller: string,
  ): Promise<{ keepRoll: number; topicIndex: bigint }> {
    const key = await this.epochHmacKey(epochState);
    const label = new TextEncoder().encode(
      `topics|${epochState.epoch}|${topLevelSite}|${caller}`,
    );
    const mac = new Uint8Array(await crypto.subtle.sign("HMAC", key, label));
    const topicIndex = readU64Be(mac, 0);
    const keepRoll = Number(readU64Be(mac, 8)) / 2 ** 64;
    return { keepRoll, topicIndex };
  }

  /** 每个纪元独立导入一次 HMAC 密钥并缓存。 */
  private epochHmacKey(epochState: EpochState): Promise<CryptoKey> {
    const cached = this.hmacKeyCache.get(epochState.epoch);
    if (cached) {
      return cached;
    }
    const keyPromise = crypto.subtle.importKey(
      "raw",
      epochState.seed,
      HMAC_ALG,
      false,
      ["sign"],
    );
    this.hmacKeyCache.set(epochState.epoch, keyPromise);
    return keyPromise;
  }
}

function readU64Be(bytes: Uint8Array, offset: number): bigint {
  let value = 0n;
  for (let i = 0; i < 8; i++) {
    value = (value << 8n) | BigInt(bytes[offset + i]);
  }
  return value;
}
