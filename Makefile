CXX ?= clang++
CXXSTD ?= -std=c++17
WARNINGS := -Wall -Wextra -Wconversion -Werror -pedantic

BUILD_DIR := build
TEST_BIN := $(BUILD_DIR)/mpk_verify_tests

.PHONY: all test clean

all: $(TEST_BIN)

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(TEST_BIN): tests/mpk_verify_tests.cpp include/mpk_verify/mpk_verify.hpp | $(BUILD_DIR)
	$(CXX) $(CXXSTD) $(WARNINGS) -Iinclude tests/mpk_verify_tests.cpp -o $@

test: $(TEST_BIN)
	./$(TEST_BIN)

clean:
	rm -rf $(BUILD_DIR)
