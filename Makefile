# Intel CET shadow-stack / IBT symbolic verifier.
# Zero external dependencies; the C++17 standard library is sufficient.

CXX      ?= c++
CXXSTD   ?= -std=c++17
WARNINGS := -Wall -Wextra -Wconversion -Wpedantic
OPT      ?= -O2
CXXFLAGS ?= $(CXXSTD) $(WARNINGS) $(OPT)
CPPFLAGS ?= -Iinclude

BUILD_DIR := build
LIB       := $(BUILD_DIR)/libcetverify.a
TEST_BIN  := $(BUILD_DIR)/cet_tests

LIB_SRCS  := src/cet.cpp
LIB_OBJS  := $(BUILD_DIR)/cet.o
TEST_SRCS := tests/cet_tests.cpp

.PHONY: all lib test clean

# Default target avoids the archiver so the suite builds in minimal toolchains.
# Run `make lib` explicitly to produce the static archive when AR is available.
all: $(TEST_BIN)

lib: $(LIB)

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(BUILD_DIR)/cet.o: src/cet.cpp include/cet/cet.h | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) $(CPPFLAGS) -c $< -o $@

$(LIB): $(LIB_OBJS)
	$(AR) rcs $@ $(LIB_OBJS)

# Compile sources directly so `make test` works without an archiver.
$(TEST_BIN): $(TEST_SRCS) $(LIB_SRCS) | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) $(CPPFLAGS) $(TEST_SRCS) $(LIB_SRCS) -o $@

test: $(TEST_BIN)
	./$(TEST_BIN)

clean:
	rm -rf $(BUILD_DIR)
