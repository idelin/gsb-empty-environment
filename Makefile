CXX ?= clang++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Wconversion -Werror -pedantic -O2
CPPFLAGS += -Iinclude
BUILD_DIR ?= build

TEST_BIN := $(BUILD_DIR)/ct_tests
DEMO_BIN := $(BUILD_DIR)/render_demo

.PHONY: all test demo clean

all: test demo

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(TEST_BIN): tests/test_constant_time.cpp include/ctir/constant_time.hpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) $(CPPFLAGS) tests/test_constant_time.cpp -o $@

$(DEMO_BIN): tests/render_demo.cpp include/ctir/constant_time.hpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) $(CPPFLAGS) tests/render_demo.cpp -o $@

test: $(TEST_BIN)
	$(TEST_BIN)

demo: $(DEMO_BIN)
	$(DEMO_BIN)

clean:
	rm -rf $(BUILD_DIR)
