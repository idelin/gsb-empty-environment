CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -pedantic -O2

all: tests

tests: tests.cpp layout_checker.hpp
	$(CXX) $(CXXFLAGS) -o $@ tests.cpp

check: tests
	./tests

clean:
	rm -f tests

.PHONY: all check clean
