import { createOrigin, type Origin } from "./origin.ts";
import { DocumentModel, SecurityError } from "./document.ts";

export interface ScenarioInput {
  /** Human-readable label, for example "without header". */
  label: string;
  /** Raw Origin-Agent-Cluster header value, or null when absent. */
  originAgentClusterHeader: string | null;
  /** Origin of the first page (for example http://a.example.com:8080). */
  originA: Origin;
  /** Origin of the second page on a different port. */
  originB: Origin;
  /** Parent domain each page will try to assign to document.domain. */
  parentDomain: string;
}

export interface AssignmentResult {
  threw: boolean;
  errorName: string | null;
  errorMessage: string | null;
  domainAfter: string;
}

export interface ScenarioVerdict {
  label: string;
  headerPresent: boolean;
  originKeyed: boolean;
  assignmentA: AssignmentResult;
  assignmentB: AssignmentResult;
  /** Strict tuple comparison result (scheme + host + port). */
  strictSameOrigin: boolean;
  /** Effective DOM-access comparison after the attempted assignment. */
  effectiveSameOrigin: boolean;
}

export interface DomainLockReport {
  parentDomain: string;
  scenarios: ScenarioVerdict[];
}

function attemptAssignment(
  doc: DocumentModel,
  parentDomain: string,
): AssignmentResult {
  try {
    doc.setDomain(parentDomain);
    return {
      threw: false,
      errorName: null,
      errorMessage: null,
      domainAfter: doc.getDomain(),
    };
  } catch (error) {
    const domError = error as SecurityError;
    return {
      threw: true,
      errorName: domError.name,
      errorMessage: domError.message,
      domainAfter: doc.getDomain(),
    };
  }
}

/**
 * Runs a single scenario: build the two pages, have each attempt one
 * `document.domain = parentDomain` assignment, then adjudicate origin
 * equality both strictly (tuple) and through the effective DOM rule.
 */
export function adjudicateScenario(input: ScenarioInput): ScenarioVerdict {
  const headerPresent = input.originAgentClusterHeader !== null;

  const docA = new DocumentModel({
    origin: input.originA,
    originAgentClusterHeader: input.originAgentClusterHeader,
  });
  const docB = new DocumentModel({
    origin: input.originB,
    originAgentClusterHeader: input.originAgentClusterHeader,
  });

  const assignmentA = attemptAssignment(docA, input.parentDomain);
  const assignmentB = attemptAssignment(docB, input.parentDomain);

  return {
    label: input.label,
    headerPresent,
    originKeyed: docA.originKeyed,
    assignmentA,
    assignmentB,
    strictSameOrigin: docA.isSameOrigin(docB),
    effectiveSameOrigin: docA.canAccess(docB),
  };
}

export interface PipelineConfig {
  scheme?: string;
  subdomainA?: string;
  subdomainB?: string;
  registrableDomain?: string;
  portA?: number;
  portB?: number;
  parentDomain?: string;
}

/**
 * The document-pipeline entry point.
 *
 * It performs the requested ruling twice over the same two different-port
 * pages:
 *   1. without the Origin-Agent-Cluster header (legacy behavior),
 *   2. with `Origin-Agent-Cluster: ?1` (origin-keyed behavior),
 * assigning the parent domain once per page in each scenario.
 */
export function runDomainLockTrial(
  config: PipelineConfig = {},
): DomainLockReport {
  const scheme = config.scheme ?? "https";
  const registrableDomain = config.registrableDomain ?? "example.com";
  const subdomainA = config.subdomainA ?? "a";
  const subdomainB = config.subdomainB ?? "b";
  const portA = config.portA ?? 8080;
  const portB = config.portB ?? 9090;
  const parentDomain = config.parentDomain ?? registrableDomain;

  const originA = createOrigin(
    scheme,
    `${subdomainA}.${registrableDomain}`,
    portA,
  );
  const originB = createOrigin(
    scheme,
    `${subdomainB}.${registrableDomain}`,
    portB,
  );

  const scenarios: ScenarioInput[] = [
    {
      label: "without Origin-Agent-Cluster header",
      originAgentClusterHeader: null,
      originA,
      originB,
      parentDomain,
    },
    {
      label: "with Origin-Agent-Cluster: ?1",
      originAgentClusterHeader: "?1",
      originA,
      originB,
      parentDomain,
    },
  ];

  return {
    parentDomain,
    scenarios: scenarios.map(adjudicateScenario),
  };
}
