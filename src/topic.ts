export interface SparkplugTopic {
  namespace: "spBv1.0";
  groupId: string;
  messageType: string;
  nodeId: string;
  deviceId?: string;
  levels: string[];
}

const NAMESPACE = "spBv1.0";

function isNonEmptyLevel(level: string): boolean {
  return level.length > 0 && !level.includes("\u0000");
}

/**
 * Parse a concrete Sparkplug B topic. Wildcards are rejected: a publish topic
 * must always name the group, message type, and node explicitly.
 */
export function parseSparkplugTopic(topic: string): SparkplugTopic | null {
  if (typeof topic !== "string" || topic.length === 0) return null;
  if (topic.includes("\u0000") || topic.includes("+") || topic.includes("#")) {
    return null;
  }

  const levels = topic.split("/");
  if (levels[0] !== NAMESPACE) return null;
  if (!levels.every(isNonEmptyLevel)) return null;

  if (levels[1] === "STATE" && levels.length === 3) {
    return {
      namespace: NAMESPACE,
      groupId: "STATE",
      messageType: "STATE",
      nodeId: levels[2],
      levels,
    };
  }

  const levelCount = levels.length;
  const messageType = levels[2];
  const knownMessageType =
    messageType === "NBIRTH" ||
    messageType === "NDEATH" ||
    messageType === "NCMD" ||
    messageType === "NDATA" ||
    messageType === "DBIRTH" ||
    messageType === "DDATA" ||
    messageType === "DDEATH" ||
    messageType === "DCMD";
  const validShape =
    ((messageType === "NBIRTH" ||
      messageType === "NDEATH" ||
      messageType === "NCMD") &&
      levelCount === 4) ||
    (messageType === "NDATA" && (levelCount === 4 || levelCount === 5)) ||
    ((messageType === "DBIRTH" ||
      messageType === "DDATA" ||
      messageType === "DDEATH" ||
      messageType === "DCMD") &&
      levelCount === 5);

  if (knownMessageType && !validShape) return null;
  // Unknown message types are parsed so callers can produce a precise denial.
  if (!knownMessageType && levelCount !== 4 && levelCount !== 5) return null;

  const result: SparkplugTopic = {
    namespace: NAMESPACE,
    groupId: levels[1],
    messageType,
    nodeId: levels[3],
    levels,
  };

  if (levels.length === 5) result.deviceId = levels[4];
  return result;
}

/** True when a topic is in the Sparkplug namespace and has a valid shape. */
export function isSparkplugTopic(topic: string): boolean {
  return parseSparkplugTopic(topic) !== null;
}

export interface TopicFilter {
  levels: string[];
  concrete: boolean;
}

/** Validate MQTT topic-filter syntax and return its levels. */
export function parseTopicFilter(filter: string): TopicFilter | null {
  if (typeof filter !== "string" || filter.length === 0) return null;
  if (filter.includes("\u0000")) return null;

  const levels = filter.split("/");
  if (!levels.every(isNonEmptyLevel)) return null;

  let concrete = true;
  for (let index = 0; index < levels.length; index += 1) {
    const level = levels[index];
    if (level === "#") {
      concrete = false;
      if (index !== levels.length - 1) return null;
      continue;
    }
    if (level === "+") {
      concrete = false;
      continue;
    }
    if (level.includes("#") || level.includes("+")) return null;
  }

  return { levels, concrete };
}

/** Test whether a concrete topic matches an MQTT topic filter. */
export function topicMatchesFilter(topic: string, filter: string): boolean {
  const parsedFilter = parseTopicFilter(filter);
  if (parsedFilter === null || topic.includes("\u0000")) return false;

  const topicLevels = topic.split("/");
  for (let index = 0; index < parsedFilter.levels.length; index += 1) {
    const filterLevel = parsedFilter.levels[index];
    if (filterLevel === "#") return true;
    if (index >= topicLevels.length) return false;
    if (filterLevel !== "+" && filterLevel !== topicLevels[index]) return false;
  }

  return topicLevels.length === parsedFilter.levels.length;
}

export function hasMqttWildcard(value: string): boolean {
  return value.includes("+") || value.includes("#");
}

export const SPARKPLUG_NAMESPACE = NAMESPACE;
