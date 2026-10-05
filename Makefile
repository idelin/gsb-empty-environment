CXX ?= g++
CXXFLAGS ?= -O2 -std=c++17 -Wall -Wextra

atlas_solver: src/main.cpp src/checker.cpp src/checker.h
	$(CXX) $(CXXFLAGS) -o $@ src/main.cpp src/checker.cpp

clean:
	rm -f atlas_solver

.PHONY: clean
