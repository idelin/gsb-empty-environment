"""Runnable demo on synthetic data (Python 3.11+, stdlib only).

Run from the repository root:

    python3.11 -m examples.run_sample
"""
from __future__ import annotations

import json

from secexplain.cache import ExplanationCache
from secexplain.engine import Engine
from secexplain.model import PUBLIC, SECRET
from secexplain.noninterference import pairwise_check
from secexplain.projection import (PFactNode, PPlaceholder, project,
                                   project_trace)
from secexplain.replay import build_evidence, replay
from secexplain.sampledata import build_world, sample_domain


def render(node, indent=0):
    pad = "  " * indent
    if isinstance(node, PPlaceholder):
        return pad + node.token
    lines = [f"{pad}fact {node.key} = {node.value} "
             f"({node.fact_id} v{node.version})"]
    support = node.support
    if isinstance(support, PPlaceholder):
        lines.append(f"{pad}  support: {support.token}")
    elif support is not None:
        lines.append(f"{pad}  via rule {support.rule_id} "
                     f"v{support.rule_version} "
                     f"(step {support.step_id} v{support.version})")
        for child in support.children:
            lines.append(render(child, indent + 2))
    return "\n".join(lines)


def main():
    print("=" * 72)
    print("1. Public explanation of a visible conclusion with hidden premises")
    print("=" * 72)
    world = build_world(chain_len=3, extra_hidden=2)
    derivation = Engine(world.rules).derive(world.facts)
    expl = project(derivation, "approved", PUBLIC)
    print(render(expl.root))
    print(f"\ncomplete: {expl.complete}  (explanation marked incomplete)")
    print(f"structural counts: {json.dumps(expl.summary, sort_keys=True)}")
    print(f"digest: {expl.digest}")
    print()
    print("=" * 72)
    print("2. Replayable evidence")
    print("=" * 72)
    evidence = build_evidence(expl, derivation)
    print(f"evidence digest: {evidence['digest']}")
    print(f"replay( evidence ): {replay(evidence)}")
    tampered = dict(evidence, digest="0" * 64)
    print(f"replay( tampered ): {replay(tampered)}")
    print()
    print("=" * 72)
    print("3. Pairwise noninterference over a finite domain of synthetic worlds")
    print("=" * 72)
    domain = sample_domain()
    print(f"domain size: {len(domain)} low-equivalent worlds")
    naive_violations = pairwise_check(domain, "approved", PUBLIC, mode="naive")
    print(f"naive field-deletion projection: {len(naive_violations)} "
          f"violating pairs (LEAKS)")
    witness = naive_violations[0]
    for label, output in (("world " + witness.world_a, witness.output_a),
                          ("world " + witness.world_b, witness.output_b)):
        summary = json.loads(output[len("OK:"):])["summary"]
        print(f"  {label}: summary={json.dumps(summary, sort_keys=True)}")
    fixed_violations = pairwise_check(domain, "approved", PUBLIC, mode="fixed")
    print(f"fixed projection:                {len(fixed_violations)} "
          f"violating pairs (noninterfering)")
    print()
    print("=" * 72)
    print("4. Permission-scoped cache and projected debug trace")
    print("=" * 72)
    cache = ExplanationCache()
    cache.put(SECRET, "fd", "rd", "approved",
              project(derivation, "approved", SECRET))
    cache.put(PUBLIC, "fd", "rd", "approved", expl)
    print(f"cache sizes: public={cache.size(PUBLIC)} "
          f"secret={cache.size(SECRET)} "
          f"internal={cache.size(1)}")
    print("debug trace at PUBLIC clearance:")
    for event in project_trace(derivation, PUBLIC):
        print(f"  {json.dumps(event, sort_keys=True)}")


if __name__ == "__main__":
    main()
