# ARM64 EL2 hypervisor audit library - strict, zero-dependency build.
CXX ?= clang++
CXXSTD := -std=c++17
WARNINGS := -Wall -Wextra -Wconversion -Werror -Wpedantic
INCLUDES := -Iinclude

BUILD_DIR := build
LIB_SRCS := src/el2_audit.cc
TEST_SRCS := tests/test_el2_audit.cc
TEST_BIN := $(BUILD_DIR)/el2_audit_tests

.PHONY: all test clean

all: $(TEST_BIN)

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(TEST_BIN): $(LIB_SRCS) $(TEST_SRCS) | $(BUILD_DIR)
	$(CXX) $(CXXSTD) $(WARNINGS) $(INCLUDES) $(LIB_SRCS) $(TEST_SRCS) -o $@

test: $(TEST_BIN)
	./$(TEST_BIN)

clean:
	rm -rf $(BUILD_DIR)
