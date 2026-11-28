CXX ?= clang++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Wconversion -Werror
BUILD_DIR ?= build

LIB_SRC := src/audit.cpp
TEST_SRC := tests/test_audit.cpp
CLI_SRC := examples/audit_cli.cpp

.PHONY: all test cli clean

all: $(BUILD_DIR)/ext4_audit_tests $(BUILD_DIR)/ext4_audit_cli

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(BUILD_DIR)/ext4_audit_tests: $(TEST_SRC) $(LIB_SRC) tests/image_builder.h \
    include/ext4_audit/ext4_audit.h | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) -Iinclude -Itests $(TEST_SRC) $(LIB_SRC) -o $@

$(BUILD_DIR)/ext4_audit_cli: $(CLI_SRC) $(LIB_SRC) \
    include/ext4_audit/ext4_audit.h | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) -Iinclude $(CLI_SRC) $(LIB_SRC) -o $@

test: $(BUILD_DIR)/ext4_audit_tests
	$(BUILD_DIR)/ext4_audit_tests

cli: $(BUILD_DIR)/ext4_audit_cli

clean:
	rm -rf $(BUILD_DIR)
