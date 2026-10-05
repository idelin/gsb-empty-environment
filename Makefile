CXX ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra

all: isel run_tests

isel: src/main.cpp src/selector.cpp include/isel/selector.h
	$(CXX) $(CXXFLAGS) -Iinclude src/main.cpp src/selector.cpp -o $@

run_tests: tests/tests.cpp src/selector.cpp include/isel/selector.h
	$(CXX) $(CXXFLAGS) -Iinclude tests/tests.cpp src/selector.cpp -o $@

check: all
	./run_tests
	./isel < examples/fusion.txt
	./isel < examples/shared.txt
	./isel < examples/regclass.txt

clean:
	rm -f isel run_tests

.PHONY: all check clean
