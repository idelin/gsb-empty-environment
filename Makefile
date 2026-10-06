CXX ?= g++
CXXFLAGS ?= -std=c++17 -O1 -g -Wall -Wextra -Werror

build/test_gc: src/gc_graph.cpp src/gc_graph.h tests/test_gc.cpp
	@mkdir -p build
	$(CXX) $(CXXFLAGS) -Isrc src/gc_graph.cpp tests/test_gc.cpp -o $@

test: build/test_gc
	./build/test_gc

clean:
	rm -rf build

.PHONY: test clean
