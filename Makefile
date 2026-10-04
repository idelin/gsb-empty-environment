CXX ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -Wpedantic

all: dircopy unit_tests

dircopy: src/main.cpp src/copy_engine.cpp src/copy_engine.hpp
	$(CXX) $(CXXFLAGS) -o $@ src/main.cpp src/copy_engine.cpp

unit_tests: tests/unit_tests.cpp src/copy_engine.cpp src/copy_engine.hpp
	$(CXX) $(CXXFLAGS) -o $@ tests/unit_tests.cpp src/copy_engine.cpp

test: all
	./unit_tests
	sh tests/integration.sh ./dircopy

clean:
	rm -f dircopy unit_tests

.PHONY: all test clean
