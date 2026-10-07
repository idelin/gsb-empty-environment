CXX ?= c++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Werror -O1 -g

tests: src/tests.o src/object_graph.o
	$(CXX) $(CXXFLAGS) -o $@ $^

src/%.o: src/%.cpp src/object_graph.h
	$(CXX) $(CXXFLAGS) -c -o $@ $<

test: tests
	./tests

clean:
	rm -f tests src/*.o

.PHONY: test clean
