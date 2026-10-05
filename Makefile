CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Werror -O2

all: test_ringlog

ringlog.o: ringlog.cpp ringlog.h
	$(CXX) $(CXXFLAGS) -c ringlog.cpp -o $@

test_ringlog.o: test_ringlog.cpp ringlog.h
	$(CXX) $(CXXFLAGS) -c test_ringlog.cpp -o $@

test_ringlog: ringlog.o test_ringlog.o
	$(CXX) $(CXXFLAGS) $^ -o $@

test: test_ringlog
	./test_ringlog

clean:
	rm -f *.o test_ringlog

.PHONY: all test clean
