export type MemberStatus = "alive" | "suspect" | "left";

export type ChangeType = "join" | "alive" | "suspect" | "leave";

export interface ChangeEvent {
  type: ChangeType;
  nodeId: string;
  incarnation: number;
}

export interface MemberRecord {
  id: string;
  status: MemberStatus;
  incarnation: number;
}

export const EVENT_WIRE_BYTES = 24;

export function eventToStatus(type: ChangeType): MemberStatus {
  switch (type) {
    case "suspect":
      return "suspect";
    case "leave":
      return "left";
    default:
      return "alive";
  }
}

export function statusToEventType(status: MemberStatus): ChangeType {
  switch (status) {
    case "suspect":
      return "suspect";
    case "left":
      return "leave";
    default:
      return "alive";
  }
}

export function shouldApply(
  current: MemberRecord | undefined,
  event: ChangeEvent,
): boolean {
  const status = eventToStatus(event.type);
  if (!current) return true;
  if (current.status === "left") return false;
  if (status === "left") return event.incarnation >= current.incarnation;
  if (status === "suspect") {
    if (current.status === "alive") return event.incarnation >= current.incarnation;
    return event.incarnation > current.incarnation;
  }
  if (event.incarnation > current.incarnation) return true;
  return event.incarnation === current.incarnation && current.status === "suspect";
}
