CXX ?= g++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra

tri_checker: src/tri_checker.cpp src/exact.h src/geom.h
	$(CXX) $(CXXFLAGS) -o $@ src/tri_checker.cpp

test: tri_checker
	./run_tests.sh

clean:
	rm -f tri_checker

.PHONY: test clean
