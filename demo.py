"""Runnable sample session: python3 demo.py"""

from dictionary import FrequencyDictionary


def main():
    d = FrequencyDictionary(alphabet="abcdefghijklmnopqrstuvwxyz")

    for word in ["he", "hen", "her", "here", "she", "hen"]:
        d.add(word)
    print("words with prefix 'he':", d.words_with_prefix("he"))
    print("frequencies under 'he':", d.frequencies_with_prefix("he"))
    print("count('hen') =", d.count("hen"))
    print("states:", d.automaton.state_count)

    d.remove("he")
    print("\nafter remove('he'):")
    print("contains('he') =", d.contains("he"))
    print("contains('hen') =", d.contains("hen"), " contains('here') =", d.contains("here"))

    d.remove("hen")
    print("\nafter remove('hen') once (was added twice):")
    print("contains('hen') =", d.contains("hen"), " count =", d.count("hen"))

    snap = d.automaton.snapshot()
    d.remove("her")
    d.remove("here")
    print("\nsnapshot words:", snap.words())
    print("current words:", d.automaton.words())


if __name__ == "__main__":
    main()
