/**
 * 端口能力组件:供沙箱宿主管理隔离域(Domain)之间的消息端口(Port)。
 *
 * 核心语义:
 * - 端口即能力:只有端口的当前持有域才能通过它发送消息。
 * - 发送消息可附带若干端口;所有权转移与消息入队在同一同步序列内原子完成。
 *   校验失败时不产生任何副作用(消息不入队、端口归属不变、旧句柄保持有效)。
 * - 转移成功后旧句柄立即失效(归属已变更,原持有者再发送会得到 NOT_OWNER)。
 * - 同一批次或跨消息重复转移同一端口会被拒绝。
 * - 端口允许通过队列中的在途转移形成引用环;回收采用标记-清除,因此成环的
 *   组件在失去可达性后仍会被整体回收。
 * - 关闭域时回收不可达端口:从所有开放域持有的端口出发,沿"纠缠对端"边与
 *   "在途转移"边做可达性标记,未被标记的端口连同其排队消息一起销毁。
 *   纠缠的两个端点同生共死;仍可达但属于已关闭域的端口成为无主的悬空端点,
 *   发往它的消息(如迟到回复)滞留在邮箱中,直到整个组件不可达后被回收。
 */

export type DomainId = string;
export type PortId = number;
export type MessageId = number;

export type PortErrorCode =
  | 'DOMAIN_CLOSED'
  | 'DOMAIN_EXISTS'
  | 'UNKNOWN_DOMAIN'
  | 'PORT_DEAD'
  | 'NOT_OWNER'
  | 'DUPLICATE_TRANSFER'
  | 'SELF_TRANSFER';

export class PortCapabilityError extends Error {
  readonly code: PortErrorCode;

  constructor(code: PortErrorCode, message: string) {
    super(message);
    this.name = 'PortCapabilityError';
    this.code = code;
  }
}

/**
 * 可暂停队列:暂停期间 enqueue 照常缓冲,dequeue 一律返回 undefined;
 * 恢复后按 FIFO 顺序取出。用于确定性地测试"已入队但未投递"的中间状态。
 */
export class PausableQueue<T> {
  #items: T[] = [];
  #paused = false;

  get paused(): boolean {
    return this.#paused;
  }

  get size(): number {
    return this.#items.length;
  }

  /** 只读视图,供宿主做可达性遍历。 */
  get items(): readonly T[] {
    return this.#items;
  }

  pause(): void {
    this.#paused = true;
  }

  resume(): void {
    this.#paused = false;
  }

  enqueue(item: T): void {
    this.#items.push(item);
  }

  dequeue(): T | undefined {
    if (this.#paused) return undefined;
    return this.#items.shift();
  }

  /** 清空并返回被丢弃的元素(回收时使用)。 */
  clear(): T[] {
    const dropped = this.#items;
    this.#items = [];
    return dropped;
  }
}

/** 在途消息(宿主内部结构)。 */
interface Envelope {
  readonly id: MessageId;
  /** 发送端点(回复时向其邮箱投递)。 */
  readonly from: Port;
  /** 接收端点(消息进入它的邮箱)。 */
  readonly to: Port;
  readonly payload: unknown;
  /** 随消息转移的端口,入队时已改属接收方所在域。 */
  readonly transfer: readonly Port[];
}

/** 投递给域处理函数的消息视图。 */
export interface IncomingMessage {
  readonly id: MessageId;
  /** 发送端点;reply() 会把消息投递到它的邮箱。 */
  readonly from: Port;
  readonly payload: unknown;
  /** 随消息转移过来的端口,现已归接收域所有。 */
  readonly transfer: readonly Port[];
  /** 沿接收端点回发消息;若本域已不再持有该端点会抛 NOT_OWNER。 */
  reply(payload: unknown, transfer?: readonly Port[]): MessageId;
}

export class Port {
  readonly id: PortId;
  /** 以下字段由 PortHost 管理,外部只读。 */
  owner: Domain | null = null;
  peer: Port | null = null;
  alive = true;
  readonly mailbox = new PausableQueue<Envelope>();

  constructor(id: PortId) {
    this.id = id;
  }

  get isAlive(): boolean {
    return this.alive;
  }

  get ownerId(): DomainId | null {
    return this.owner?.id ?? null;
  }
}

export class Domain {
  readonly id: DomainId;
  /** 由 PortHost 管理,外部只读。 */
  open = true;
  onmessage: ((message: IncomingMessage) => void) | null = null;

  constructor(id: DomainId) {
    this.id = id;
  }
}

export class PortHost {
  #domains = new Map<DomainId, Domain>();
  #ports = new Map<PortId, Port>();
  #nextPortId = 1;
  #nextMessageId = 1;

  createDomain(id: DomainId): Domain {
    if (this.#domains.has(id)) {
      throw new PortCapabilityError('DOMAIN_EXISTS', `domain already exists: ${id}`);
    }
    const domain = new Domain(id);
    this.#domains.set(id, domain);
    return domain;
  }

  domain(id: DomainId): Domain {
    const domain = this.#domains.get(id);
    if (!domain) {
      throw new PortCapabilityError('UNKNOWN_DOMAIN', `unknown domain: ${id}`);
    }
    return domain;
  }

  /** 创建一对纠缠端口,初始归属可以相同(之后再经消息转移)。 */
  createChannel(ownerA: DomainId, ownerB: DomainId = ownerA): { port1: Port; port2: Port } {
    const a = this.domain(ownerA);
    const b = this.domain(ownerB);
    const port1 = this.#newPort(a);
    const port2 = this.#newPort(b);
    port1.peer = port2;
    port2.peer = port1;
    return { port1, port2 };
  }

  /**
   * 通过端口 via 发送消息,可附带若干端口一并转移。
   *
   * 分两个阶段执行,保证原子性:
   * 1. 校验阶段——任何失败都抛出 PortCapabilityError,且不产生任何副作用;
   * 2. 提交阶段——先改端口归属,再把消息推入对端邮箱,是一个不会被中断的
   *    同步序列,对调用者而言转移与入队同时生效。
   */
  postMessage(
    from: Domain,
    via: Port,
    payload: unknown,
    transfer: readonly Port[] = [],
  ): MessageId {
    // ---- 校验阶段 ----
    if (!from.open) {
      throw new PortCapabilityError('DOMAIN_CLOSED', `domain is closed: ${from.id}`);
    }
    if (!via.alive) {
      throw new PortCapabilityError('PORT_DEAD', `port has been reclaimed: ${via.id}`);
    }
    if (via.owner !== from) {
      throw new PortCapabilityError(
        'NOT_OWNER',
        `domain ${from.id} does not own port ${via.id}`,
      );
    }
    const seen = new Set<Port>();
    for (const port of transfer) {
      if (seen.has(port)) {
        throw new PortCapabilityError(
          'DUPLICATE_TRANSFER',
          `port ${port.id} appears twice in the same transfer list`,
        );
      }
      seen.add(port);
      if (port === via) {
        throw new PortCapabilityError(
          'SELF_TRANSFER',
          `cannot transfer the port ${via.id} through itself`,
        );
      }
      if (!port.alive) {
        throw new PortCapabilityError('PORT_DEAD', `port has been reclaimed: ${port.id}`);
      }
      if (port.owner !== from) {
        throw new PortCapabilityError(
          'NOT_OWNER',
          `domain ${from.id} does not own port ${port.id}`,
        );
      }
    }

    // ---- 提交阶段(同步、不可中断):转移归属与消息入队原子完成 ----
    const target = via.peer;
    if (!target || !target.alive) {
      // 活着的端口其对端必然活着(纠缠端点同生共死),此处只是防御。
      throw new PortCapabilityError('PORT_DEAD', `peer of port ${via.id} is gone`);
    }
    const recipient = target.owner; // 可能为 null(对端是悬空端口),消息将滞留
    for (const port of transfer) {
      port.owner = recipient;
    }
    const envelope: Envelope = {
      id: this.#nextMessageId++,
      from: via,
      to: target,
      payload,
      transfer: [...transfer],
    };
    target.mailbox.enqueue(envelope);
    return envelope.id;
  }

  /**
   * 投递消息:把指定域(缺省为全部开放域)名下端口邮箱中的消息逐条派发给
   * 其 onmessage。暂停中的邮箱不会被取走。返回投递条数。
   */
  pump(domainId?: DomainId): number {
    const domains =
      domainId === undefined ? [...this.#domains.values()] : [this.domain(domainId)];
    let delivered = 0;
    for (const domain of domains) {
      if (!domain.open) continue;
      for (const port of this.#portsOf(domain)) {
        let envelope: Envelope | undefined;
        while ((envelope = port.mailbox.dequeue()) !== undefined) {
          this.#deliver(domain, port, envelope);
          delivered += 1;
        }
      }
    }
    return delivered;
  }

  /**
   * 关闭域:域立即不可再发送;其名下端口改为无主,随后做可达性回收。
   * 幂等,重复调用无副作用。
   */
  closeDomain(id: DomainId): void {
    const domain = this.domain(id);
    if (!domain.open) return;
    domain.open = false;
    for (const port of this.#ports.values()) {
      if (port.owner === domain) port.owner = null;
    }
    this.#collectGarbage();
  }

  /** 仍存活的端口(诊断/测试用)。 */
  get alivePorts(): readonly Port[] {
    return [...this.#ports.values()].filter((port) => port.alive);
  }

  #newPort(owner: Domain): Port {
    const port = new Port(this.#nextPortId++);
    port.owner = owner;
    this.#ports.set(port.id, port);
    return port;
  }

  #portsOf(domain: Domain): Port[] {
    return [...this.#ports.values()].filter((port) => port.owner === domain);
  }

  #deliver(domain: Domain, port: Port, envelope: Envelope): void {
    const incoming: IncomingMessage = {
      id: envelope.id,
      from: envelope.from,
      payload: envelope.payload,
      transfer: envelope.transfer,
      reply: (payload, transfer = []) => this.postMessage(domain, port, payload, transfer),
    };
    domain.onmessage?.(incoming);
  }

  /**
   * 标记-清除回收:以开放域持有的端口为根,沿纠缠边与在途转移边遍历。
   * 因为基于可达性而非引用计数,端口环在失去外部可达性后会被整体回收。
   */
  #collectGarbage(): void {
    const marked = new Set<Port>();
    const stack: Port[] = [];
    for (const port of this.#ports.values()) {
      if (port.alive && port.owner !== null && port.owner.open) stack.push(port);
    }
    while (stack.length > 0) {
      const port = stack.pop()!;
      if (marked.has(port) || !port.alive) continue;
      marked.add(port);
      if (port.peer) stack.push(port.peer);
      for (const envelope of port.mailbox.items) {
        for (const transferred of envelope.transfer) stack.push(transferred);
      }
    }
    for (const port of this.#ports.values()) {
      if (port.alive && !marked.has(port)) this.#destroy(port);
    }
  }

  #destroy(port: Port): void {
    port.alive = false;
    port.owner = null;
    port.mailbox.clear();
  }
}
