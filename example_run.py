import math
import tempfile
from pathlib import Path

from dictmerge import (
    Column,
    DictBatch,
    EncodedColumn,
    LocalDictionary,
    MergeJob,
    TermSpec,
    decode_with_dictionary,
    split_batch_random,
)


word = Column("word", "string")
amount = Column("amount", "int")

first_terms = (TermSpec("a", "a"), TermSpec("b", "b"), TermSpec("empty", ""))
second_terms = (TermSpec("b", "b"), TermSpec("a", "a"))
amount_terms = (TermSpec("exact", 2**63 + 17),)

first = DictBatch(
    0,
    {
        word.name: LocalDictionary("batch-0-v1", {word: first_terms}),
        amount.name: LocalDictionary("batch-0-v1", {amount: amount_terms}),
    },
    (
        EncodedColumn(word, (0, 1, None, 2)),
        EncodedColumn(amount, (0, 0, None, 0)),
    ),
)
second = DictBatch(
    1,
    {
        word.name: LocalDictionary("batch-1-v1", {word: second_terms}),
        amount.name: LocalDictionary("batch-1-v1", {amount: ()}),
    },
    (
        EncodedColumn(word, (0, 1, 0)),
        EncodedColumn(amount, (None, None, None)),
    ),
)

reference = {
    name: decode_with_dictionary(first)[name] + decode_with_dictionary(second)[name]
    for name, _ in ((word.name, word.value_type), (amount.name, amount.value_type))
}


def main():
    with tempfile.TemporaryDirectory() as directory:
        workdir = Path(directory) / "merge-job"
        job = MergeJob(workdir, [first, second], memory_limit_bytes=96)
        chunks = split_batch_random(first, seed=7) + split_batch_random(second, seed=11)
        for chunk in chunks:
            job.add_chunk(chunk)
        result = job.finalize()
        decoded = result.decode()

        print("random chunks:", [(chunk.batch_index, chunk.start_row, chunk.row_count) for chunk in chunks])
        print("reference   :", reference)
        print("merged      :", decoded)
        print("equal       :", decoded == reference)
        print("dictionary  :", result.dictionary_path)
        print("recoded     :", result.recoded_batch_path)
        print("accounting  :", result.accounting_dict())


if __name__ == "__main__":
    main()
