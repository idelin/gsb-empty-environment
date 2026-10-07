CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Wpedantic -Werror -g -fsanitize=address,undefined

test_small_vector: test_small_vector.cpp small_vector.hpp
	$(CXX) $(CXXFLAGS) -o $@ test_small_vector.cpp

test: test_small_vector
	./test_small_vector

clean:
	rm -f test_small_vector

.PHONY: test clean
