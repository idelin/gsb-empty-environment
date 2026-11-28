CXX ?= clang++
CXXSTD ?= -std=c++17
WARNINGS := -Wall -Wextra -Wconversion -Wsign-conversion -Werror
INCLUDES := -Iinclude -Itests

BUILD_DIR := build
LIB_SRC := src/reghive_audit.cpp
TEST_SRC := tests/test_reghive_audit.cpp

.PHONY: all test clean

all: $(BUILD_DIR)/test_reghive_audit

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(BUILD_DIR)/reghive_audit.o: $(LIB_SRC) include/reghive_audit.hpp | $(BUILD_DIR)
	$(CXX) $(CXXSTD) $(WARNINGS) $(INCLUDES) -c $(LIB_SRC) -o $@

$(BUILD_DIR)/test_reghive_audit.o: $(TEST_SRC) include/reghive_audit.hpp tests/hive_builder.hpp | $(BUILD_DIR)
	$(CXX) $(CXXSTD) $(WARNINGS) $(INCLUDES) -c $(TEST_SRC) -o $@

$(BUILD_DIR)/test_reghive_audit: $(BUILD_DIR)/test_reghive_audit.o $(BUILD_DIR)/reghive_audit.o
	$(CXX) $(CXXSTD) $(WARNINGS) $(INCLUDES) $^ -o $@

test: $(BUILD_DIR)/test_reghive_audit
	./$(BUILD_DIR)/test_reghive_audit

clean:
	rm -rf $(BUILD_DIR)
