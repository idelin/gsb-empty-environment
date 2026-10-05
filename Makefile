CXX ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -Wpedantic

tests: tests.cpp scaler.hpp
	$(CXX) $(CXXFLAGS) -o $@ tests.cpp

check: tests
	./tests

clean:
	rm -f tests

.PHONY: check clean
