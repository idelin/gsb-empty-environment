"""Run the synthetic sample scenario and print the results."""
import json

from evengine import digest, replay
from sample_data import STEPS


def main():
    outcome = replay(STEPS)
    for evaluation in outcome["evaluations"]:
        print(f"[{evaluation['step_id']} v{evaluation['step_version']}] "
              f"rule {evaluation['rule_id']} v{evaluation['rule_version']} "
              f"domain {evaluation['domain']}")
        print(f"  satisfied intervals: {evaluation['satisfied']}")
        print(f"  gaps:                {evaluation['gaps']}")
        print(f"  longest interval:    {evaluation['longest']}")
    for report in outcome["subset_reports"]:
        print(f"[{report['step_id']} v{report['step_version']}] "
              f"minimal subset for target {report['target']}")
        print(f"  proven optimal: {report['proven_optimal']}, "
              f"size: {report['optimal_size']}, subset: {report['optimal']}")
        print(f"  enumerated {report['enumerated']} subsets "
              f"(budget {report['budget']})")
    print(f"evidence in store: {outcome['evidence_ids']}")
    print(f"replay digest: {digest(outcome)}")
    print()
    print(json.dumps(outcome, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
