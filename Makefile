CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Werror -O2

all: layout_check tests

layout_check: main.cpp layout.cpp layout.hpp
	$(CXX) $(CXXFLAGS) -o $@ main.cpp layout.cpp

tests: tests.cpp layout.cpp layout.hpp
	$(CXX) $(CXXFLAGS) -o $@ tests.cpp layout.cpp

check: tests
	./tests

clean:
	rm -f layout_check tests

.PHONY: all check clean
