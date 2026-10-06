#!/bin/sh
# 用地址/线程检测器分别构建并运行测试。
set -e
CXX="${CXX:-clang++}"
FLAGS="-std=c++20 -Wall -Wextra -O1 -g -pthread"

echo "== AddressSanitizer =="
$CXX $FLAGS -fsanitize=address test_intrusive_queue.cpp -o test_asan
./test_asan

echo "== ThreadSanitizer =="
$CXX $FLAGS -fsanitize=thread test_intrusive_queue.cpp -o test_tsan
./test_tsan
