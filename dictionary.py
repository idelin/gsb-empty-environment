"""Facade combining the minimal automaton with the frequency table.

Separation of concerns:
  * prefix / membership queries  -> MinimalWordAutomaton (structure only)
  * frequency queries            -> FrequencyTable (metadata only)
The automaton is updated only when a word's count crosses 0 <-> 1.
"""

from __future__ import annotations

from dawg import MinimalWordAutomaton
from metadata import FrequencyTable


class FrequencyDictionary:
    def __init__(self, alphabet):
        self.automaton = MinimalWordAutomaton(alphabet)
        self.frequencies = FrequencyTable()

    def add(self, word):
        """Add one occurrence of word; repeated adds accumulate."""
        self.automaton.add(word)  # validates alphabet; no-op if present
        self.frequencies.add(word)

    def remove(self, word):
        """Remove one occurrence; the word leaves the automaton at count 0."""
        self.automaton._check_word(word)
        if not self.frequencies.remove(word):
            return False
        if self.frequencies.count(word) == 0:
            self.automaton.remove(word)
        return True

    def contains(self, word):
        return self.automaton.contains(word)

    def count(self, word):
        return self.frequencies.count(word)

    def words_with_prefix(self, prefix):
        """Structural prefix query (automaton only, no metadata)."""
        return self.automaton.words_with_prefix(prefix)

    def frequencies_with_prefix(self, prefix):
        """Prefix query joined with metadata, on demand."""
        return [(w, self.frequencies.count(w)) for w in self.automaton.words_with_prefix(prefix)]

    def __len__(self):
        return len(self.automaton)
