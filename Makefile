# Zero-dependency build for the user-namespace security audit library.

CXX ?= clang++
CXXSTD := -std=c++17
WARN := -Wall -Wextra -Wconversion -Wpedantic -Wshadow -Wsign-conversion
OPT := -O2
CXXFLAGS := $(CXXSTD) $(WARN) -Werror $(OPT) -Iinclude

BUILD_DIR := build
LIB_SRC := src/userns_security.cpp
LIB_OBJ := $(BUILD_DIR)/userns_security.o
TEST_SRC := tests/test_userns_security.cpp
TEST_BIN := $(BUILD_DIR)/userns_security_tests
DEMO_SRC := examples/embed_demo.cpp
DEMO_BIN := $(BUILD_DIR)/embed_demo

.PHONY: all test demo clean

all: $(LIB_OBJ) $(DEMO_BIN)

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(LIB_OBJ): $(LIB_SRC) include/userns_security.hpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) -c $(LIB_SRC) -o $@

$(TEST_BIN): $(TEST_SRC) $(LIB_SRC) include/userns_security.hpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) $(TEST_SRC) $(LIB_SRC) -o $@

$(DEMO_BIN): $(DEMO_SRC) $(LIB_SRC) include/userns_security.hpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) $(DEMO_SRC) $(LIB_SRC) -o $@

test: $(TEST_BIN)
	./$(TEST_BIN)

clean:
	rm -rf $(BUILD_DIR)
