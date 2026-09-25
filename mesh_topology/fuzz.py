"""Randomized edit driver.

Applies a seeded random sequence of transactional edits and, after every
single edit, verifies the incrementally maintained structures against a
full rebuild (``verify_consistency``). Any mismatch raises MeshError and
the surrounding transaction/snapshot machinery restores the original
topology.
"""
from __future__ import annotations

import random


def random_edits(mesh, steps, seed=0, pool_size=8):
    rng = random.Random(seed)
    log = []
    for _ in range(steps):
        choices = ["insert"]
        if mesh.faces:
            choices += ["delete", "flip"]
        if mesh.edge_faces:
            choices += ["split_edge"]
        op = rng.choice(choices)
        if op == "insert":
            face = tuple(rng.randrange(pool_size) for _ in range(3))
            fid = mesh.insert_face(*face)
            log.append(("insert", face, fid))
        elif op == "delete":
            fid = rng.choice(list(mesh.faces))
            mesh.delete_face(fid)
            log.append(("delete", fid))
        elif op == "flip":
            fid = rng.choice(list(mesh.faces))
            mesh.flip_face(fid)
            log.append(("flip", fid))
        else:
            key = rng.choice(list(mesh.edge_faces))
            new_vid = mesh.split_edge(*key)
            log.append(("split_edge", key, new_vid))
        mesh.verify_consistency()
    return log
