"""CLI: python -m cardian input.json [--budget N] [--state S] [--save-state O]

The input JSON has keys: "stats", "query", optional "config", optional
"budget".  The analysis result is printed to stdout; if the search was
interrupted by the budget, the resumable state is written with --save-state.
"""
import argparse
import json
import sys

from .analyze import analyze


def main(argv=None):
    parser = argparse.ArgumentParser(prog="cardian")
    parser.add_argument("input", help="JSON file with stats/query/config/budget")
    parser.add_argument("--budget", type=int, default=None, help="node budget")
    parser.add_argument("--state", default=None, help="resume from saved state JSON")
    parser.add_argument("--save-state", default=None, help="write resumable state here")
    args = parser.parse_args(argv)

    with open(args.input, encoding="utf-8") as fh:
        payload = json.load(fh)
    state = None
    if args.state:
        with open(args.state, encoding="utf-8") as fh:
            state = json.load(fh)

    result = analyze(
        payload["stats"],
        payload["query"],
        config=payload.get("config"),
        budget=args.budget if args.budget is not None else payload.get("budget"),
        state=state,
    )
    saved = result.pop("state", None)
    json.dump(result, sys.stdout, indent=2, sort_keys=False)
    sys.stdout.write("\n")
    if args.save_state and saved is not None:
        with open(args.save_state, "w", encoding="utf-8") as fh:
            json.dump(saved, fh)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
