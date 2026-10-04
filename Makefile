CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -O2

all: test_chunkio

test_chunkio: chunkio.o test_chunkio.o
	$(CXX) $(CXXFLAGS) -o $@ $^

chunkio.o: chunkio.cpp chunkio.h
	$(CXX) $(CXXFLAGS) -c -o $@ $<

test_chunkio.o: test_chunkio.cpp chunkio.h
	$(CXX) $(CXXFLAGS) -c -o $@ $<

test: test_chunkio
	./test_chunkio

clean:
	rm -f *.o test_chunkio

.PHONY: all test clean
