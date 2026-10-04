CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Werror -O2 -pthread -Iinclude

HEADERS := include/drc/epoch_reclaimer.hpp include/drc/deferred_cell.hpp

build/reclamation_test: tests/reclamation_test.cpp $(HEADERS)
	@mkdir -p build
	$(CXX) $(CXXFLAGS) -o $@ tests/reclamation_test.cpp

test: build/reclamation_test
	./build/reclamation_test

clean:
	rm -rf build

.PHONY: test clean
