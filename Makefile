CXX ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -Wpedantic

BIN = dircopy
TEST = test_dircopy

all: $(BIN) $(TEST)

$(BIN): src/main.o src/dircopy.o
	$(CXX) $(CXXFLAGS) -o $@ $^

$(TEST): tests/test_dircopy.o src/dircopy.o
	$(CXX) $(CXXFLAGS) -o $@ $^

src/%.o: src/%.cpp src/dircopy.h
	$(CXX) $(CXXFLAGS) -c -o $@ $<

tests/%.o: tests/%.cpp src/dircopy.h
	$(CXX) $(CXXFLAGS) -c -o $@ $<

check: $(TEST)
	./$(TEST)

clean:
	rm -f $(BIN) $(TEST) src/*.o tests/*.o

.PHONY: all check clean
