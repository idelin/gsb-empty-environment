CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Werror -O2 -Iinclude

LIB_SRCS := src/posix_adapter.cpp src/sparse_copy.cpp
LIB_OBJS := $(LIB_SRCS:.cpp=.o)

all: sparse-copy sparse-copy-tests

sparse-copy: $(LIB_OBJS) src/main.o
	$(CXX) $(CXXFLAGS) -o $@ $^

sparse-copy-tests: $(LIB_OBJS) tests/test_main.o
	$(CXX) $(CXXFLAGS) -o $@ $^

%.o: %.cpp
	$(CXX) $(CXXFLAGS) -c -o $@ $<

check: sparse-copy-tests
	./sparse-copy-tests

clean:
	rm -f sparse-copy sparse-copy-tests $(LIB_OBJS) src/main.o tests/test_main.o

.PHONY: all check clean
