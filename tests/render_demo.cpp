#include "ctir/constant_time.hpp"

#include <cstdio>

using namespace ctir;

int main() {
    Function function;
    function.name = "sbox_demo";
    function.params = {
        Parameter{"key", Label::Secret},
        Parameter{"tbl", Label::Public},
    };
    Block block;
    block.id = 0;
    block.name = "entry";
    block.instrs = {
        Instruction::binary(1, Op::Add, "addr", "tbl", "key"),
        Instruction::load(2, "v", "addr"),
    };
    function.blocks.push_back(block);

    Report report = Analyzer(std::move(function)).run();
    std::printf("%s", Analyzer::renderReport(report).c_str());
    return 0;
}
