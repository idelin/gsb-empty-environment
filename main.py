"""CLI: minimal-loss k-anonymous full-domain generalization (synthetic data).

Examples:
    python3.11 main.py --k 4 --records 120 --seed 7            # exact search
    python3.11 main.py --k 4 --records 120 --budget 25         # budget-truncated
    python3.11 main.py --k 4 --records 120 --brute-force       # cross-check
"""

from __future__ import annotations

import argparse
import json

from kano.loss import LossModel
from kano.search import brute_force, search
from kano.synth import default_hierarchies, generate_records
from kano.verify import verify_result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--k", type=int, required=True, help="k-anonymity threshold")
    parser.add_argument("--records", type=int, default=120, help="synthetic record count")
    parser.add_argument("--seed", type=int, default=7, help="synthetic data seed")
    parser.add_argument("--budget", type=int, default=None,
                        help="max level vectors evaluated (omit for exact search)")
    parser.add_argument("--brute-force", action="store_true",
                        help="also run independent exhaustive enumeration")
    args = parser.parse_args()

    hierarchies = default_hierarchies()
    records = generate_records(args.records, hierarchies, seed=args.seed)
    loss_model = LossModel(hierarchies)

    result = search(records, hierarchies, loss_model, k=args.k, budget=args.budget)
    verified = verify_result(records, hierarchies, result, k=args.k)

    report = {
        "params": {"k": args.k, "records": args.records, "seed": args.seed,
                   "budget": args.budget},
        "fields": [h.field for h in hierarchies],
        "max_levels": [h.max_level for h in hierarchies],
        "status": result.status.value,
        "exhaustive": result.exhaustive,
        "levels": result.levels,
        "loss": result.loss,
        "equivalence_class_sizes": result.equivalence_class_sizes,
        "nodes_visited": result.nodes_visited,
        "verified": verified,
    }
    if args.brute_force:
        bf = brute_force(records, hierarchies, loss_model, k=args.k)
        report["brute_force"] = {
            "status": bf.status.value,
            "levels": bf.levels,
            "loss": bf.loss,
            "nodes_visited": bf.nodes_visited,
            "matches_search": bf.levels == result.levels and bf.loss == result.loss,
        }
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
