# Zero-dependency build.  The library itself is header-only; these targets
# compile the demo and the three test suites with the required strict flags.
CXX ?= clang++
CXXSTD := -std=c++17
WARN := -Wall -Wextra -Wconversion -Wsign-conversion -Werror -pedantic
INCLUDES := -Iinclude -Itests
BUILD := build

TESTS := $(BUILD)/test_auditor $(BUILD)/test_marshal $(BUILD)/test_adversary

.PHONY: all test clean

all: $(BUILD)/ccm_demo $(TESTS)

$(BUILD):
	mkdir -p $(BUILD)

$(BUILD)/ccm_demo: demo/main.cpp | $(BUILD)
	$(CXX) $(CXXSTD) $(WARN) -Iinclude $< -o $@

$(BUILD)/test_%: tests/test_%.cpp tests/test_framework.hpp | $(BUILD)
	$(CXX) $(CXXSTD) $(WARN) $(INCLUDES) $< -o $@

test: $(TESTS)
	@for binary in $(TESTS); do \
		echo "running $$binary"; \
		$$binary >/dev/null || exit 1; \
	done
	@echo "all ccm tests passed"

clean:
	rm -f $(BUILD)/ccm_demo $(TESTS)
