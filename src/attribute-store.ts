import type { AttributeSpec, AttributeValue } from './policy.ts';

export interface AttributeChangeEvent {
  subjectId: string;
  attribute: string;
  kind: 'set' | 'delete';
}

export type AttributeChangeListener = (event: AttributeChangeEvent) => void;

interface TrackedCell {
  value: AttributeValue;
  /** 每次写入递增；删除后保留墓碑版本，使“已读取属性被删除”可被检测 */
  version: number;
  present: boolean;
}

/**
 * 主体属性仓库（进程内，不接外部身份服务）。
 * 每个属性独立版本号：未读取的属性变更不影响任何缓存条目。
 */
export class AttributeStore {
  private readonly cells = new Map<string, Map<string, TrackedCell>>();
  private readonly listeners = new Set<AttributeChangeListener>();
  private readonly schema: ReadonlyMap<string, AttributeSpec> | undefined;

  constructor(schema?: readonly AttributeSpec[]) {
    this.schema = schema ? new Map(schema.map((s) => [s.name, s])) : undefined;
  }

  onChange(listener: AttributeChangeListener): void {
    this.listeners.add(listener);
  }

  set(subjectId: string, attribute: string, value: AttributeValue): void {
    this.checkType(attribute, value);
    const cell = this.ensureCell(subjectId, attribute);
    if (cell.present && cell.value === value) return; // 值未变化，不产生失效
    cell.value = value;
    cell.version += 1;
    cell.present = true;
    this.emit({ subjectId, attribute, kind: 'set' });
  }

  delete(subjectId: string, attribute: string): void {
    const cell = this.cells.get(subjectId)?.get(attribute);
    if (!cell || !cell.present) return;
    cell.present = false;
    cell.version += 1; // 墓碑版本递增：读过该属性的缓存条目必须失效
    this.emit({ subjectId, attribute, kind: 'delete' });
  }

  /**
   * 读取属性并返回当前版本。缺失属性的版本视为 0，
   * 之后被写入（版本变为 1）同样能被版本比对发现。
   */
  readWithVersion(
    subjectId: string,
    attribute: string,
  ): { present: boolean; value: AttributeValue | undefined; version: number } {
    const cell = this.cells.get(subjectId)?.get(attribute);
    if (!cell || !cell.present) return { present: false, value: undefined, version: 0 };
    return { present: true, value: cell.value, version: cell.version };
  }

  private ensureCell(subjectId: string, attribute: string): TrackedCell {
    let subject = this.cells.get(subjectId);
    if (!subject) {
      subject = new Map();
      this.cells.set(subjectId, subject);
    }
    let cell = subject.get(attribute);
    if (!cell) {
      cell = { value: false, version: 0, present: false };
      subject.set(attribute, cell);
    }
    return cell;
  }

  private checkType(attribute: string, value: AttributeValue): void {
    if (!this.schema) return;
    const spec = this.schema.get(attribute);
    if (!spec) throw new Error(`属性 "${attribute}" 未在策略中显式声明，拒绝写入`);
    const ok =
      (spec.type === 'string' && typeof value === 'string') ||
      (spec.type === 'integer' && Number.isInteger(value)) ||
      (spec.type === 'boolean' && typeof value === 'boolean');
    if (!ok) throw new Error(`属性 "${attribute}" 的值与声明类型 ${spec.type} 不符`);
  }

  private emit(event: AttributeChangeEvent): void {
    for (const listener of this.listeners) listener(event);
  }
}
