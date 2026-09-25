"""Communicating-class analysis: Tarjan SCC, closed classes, transience."""

from __future__ import annotations

from dataclasses import dataclass


def tarjan_scc(n, adj):
    """Iterative Tarjan strongly-connected-components algorithm.

    ``adj[i]`` is an iterable of successor indices.  Returns a list of
    components, each a list of state indices.
    """
    index_of = [None] * n
    lowlink = [0] * n
    on_stack = [False] * n
    stack = []
    counter = 0
    components = []
    for start in range(n):
        if index_of[start] is not None:
            continue
        index_of[start] = lowlink[start] = counter
        counter += 1
        stack.append(start)
        on_stack[start] = True
        work = [(start, iter(adj[start]))]
        while work:
            node, it = work[-1]
            descended = False
            for w in it:
                if index_of[w] is None:
                    index_of[w] = lowlink[w] = counter
                    counter += 1
                    stack.append(w)
                    on_stack[w] = True
                    work.append((w, iter(adj[w])))
                    descended = True
                    break
                if on_stack[w] and index_of[w] < lowlink[node]:
                    lowlink[node] = index_of[w]
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                if lowlink[node] < lowlink[parent]:
                    lowlink[parent] = lowlink[node]
            if lowlink[node] == index_of[node]:
                component = []
                while True:
                    w = stack.pop()
                    on_stack[w] = False
                    component.append(w)
                    if w == node:
                        break
                components.append(component)
    return components


@dataclass(frozen=True)
class ClosedClass:
    """A closed communicating class (no transitions lead out of it)."""

    index: int
    states: tuple
    is_absorbing_state: bool  # singleton closed class (self-loop probability 1)


@dataclass(frozen=True)
class ChainStructure:
    """Structural decomposition of a Markov chain."""

    n: int
    classes: tuple          # tuple of frozenset, one per communicating class
    class_of: tuple         # state -> class index
    is_closed: tuple        # class index -> bool
    closed_classes: tuple   # tuple of ClosedClass
    transient_states: tuple
    irreducible: bool

    def summary(self):
        return {
            "states": self.n,
            "communicating_classes": len(self.classes),
            "irreducible": self.irreducible,
            "closed_classes": [list(cc.states) for cc in self.closed_classes],
            "absorbing_states": [
                cc.states[0] for cc in self.closed_classes if cc.is_absorbing_state
            ],
            "non_absorbing_closed_classes": [
                list(cc.states)
                for cc in self.closed_classes
                if not cc.is_absorbing_state
            ],
            "transient_states": list(self.transient_states),
        }


def classify(matrix):
    """Compute the :class:`ChainStructure` of a SparseTransitionMatrix."""
    n = matrix.n
    adj = [sorted(line) for line in matrix.lines]
    components = tarjan_scc(n, adj)
    class_of = [0] * n
    for ci, component in enumerate(components):
        for s in component:
            class_of[s] = ci
    is_closed = [True] * len(components)
    for i in range(n):
        ci = class_of[i]
        for j in matrix.lines[i]:
            if class_of[j] != ci:
                is_closed[ci] = False
                break
    closed_classes = []
    transient = []
    for ci, component in enumerate(components):
        if is_closed[ci]:
            states = tuple(sorted(component))
            closed_classes.append(
                ClosedClass(len(closed_classes), states, len(states) == 1)
            )
        else:
            transient.extend(sorted(component))
    return ChainStructure(
        n=n,
        classes=tuple(frozenset(c) for c in components),
        class_of=tuple(class_of),
        is_closed=tuple(is_closed),
        closed_classes=tuple(closed_classes),
        transient_states=tuple(transient),
        irreducible=len(components) == 1,
    )
