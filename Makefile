CXX      ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -Wpedantic
CPPFLAGS += -Iinclude

BUILD := build
LIB   := $(BUILD)/libediff.a
TEST  := $(BUILD)/test_quantize

.PHONY: all test clean

all: $(LIB) $(TEST)

$(BUILD):
	mkdir -p $(BUILD)

$(BUILD)/quantize.o: src/quantize.cpp include/ediff/quantize.h | $(BUILD)
	$(CXX) $(CPPFLAGS) $(CXXFLAGS) -c $< -o $@

$(LIB): $(BUILD)/quantize.o
	ar rcs $@ $^

$(BUILD)/test_quantize.o: tests/test_quantize.cpp include/ediff/quantize.h | $(BUILD)
	$(CXX) $(CPPFLAGS) $(CXXFLAGS) -c $< -o $@

$(TEST): $(BUILD)/test_quantize.o $(LIB)
	$(CXX) $(CXXFLAGS) $^ -o $@

test: $(TEST)
	$(TEST)

clean:
	rm -rf $(BUILD)
