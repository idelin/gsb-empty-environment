CXX ?= g++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra

stack_checker: stack_checker.cpp
	$(CXX) $(CXXFLAGS) -o $@ $<

clean:
	rm -f stack_checker

.PHONY: clean
