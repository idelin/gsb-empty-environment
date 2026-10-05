CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -O2 -Isrc

BUILD := build
LIB_OBJS := $(BUILD)/posix_adapter.o $(BUILD)/sparse_copy.o

.PHONY: all test clean

all: $(BUILD)/spcopy_tests

$(BUILD):
	mkdir -p $(BUILD)

$(BUILD)/%.o: src/%.cpp src/posix_adapter.h src/sparse_copy.h | $(BUILD)
	$(CXX) $(CXXFLAGS) -c $< -o $@

$(BUILD)/test_main.o: tests/test_main.cpp tests/fake_adapter.h src/sparse_copy.h src/posix_adapter.h | $(BUILD)
	$(CXX) $(CXXFLAGS) -Itests -c $< -o $@

$(BUILD)/spcopy_tests: $(LIB_OBJS) $(BUILD)/test_main.o
	$(CXX) $(CXXFLAGS) $^ -o $@

test: $(BUILD)/spcopy_tests
	./$(BUILD)/spcopy_tests

clean:
	rm -rf $(BUILD)
