CXX ?= clang++
CXXSTD := -std=c++17
WARNINGS := -Wall -Wextra -Wconversion -Wsign-conversion -Wshadow -pedantic -Werror
INCLUDES := -Iinclude

BUILD_DIR ?= build
TEST_BIN := $(BUILD_DIR)/ffi_safety_tests
HEADER_BIN := $(BUILD_DIR)/ffi_safety_header_check

.PHONY: all test clean

all: test

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(TEST_BIN): tests/ffi_safety_tests.cpp include/ffi_safety/ffi_safety.hpp | $(BUILD_DIR)
	$(CXX) $(CXXSTD) $(WARNINGS) $(INCLUDES) $< -o $@

$(HEADER_BIN): tests/header_check.cpp include/ffi_safety/ffi_safety.hpp | $(BUILD_DIR)
	$(CXX) $(CXXSTD) $(WARNINGS) $(INCLUDES) $< -o $@

test: $(TEST_BIN) $(HEADER_BIN)
	$(HEADER_BIN)
	$(TEST_BIN)

clean:
	rm -rf $(BUILD_DIR)
