CXX ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -pedantic

test_resampler: test_resampler.cpp resampler.hpp
	$(CXX) $(CXXFLAGS) -o $@ test_resampler.cpp

.PHONY: test clean
test: test_resampler
	./test_resampler

clean:
	rm -f test_resampler
