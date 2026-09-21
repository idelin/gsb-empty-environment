from minihm.astnodes import Node
from minihm.parser import Parser
from minihm.source import Source


def parse(code: str) -> Node:
    return Parser(Source(code)).parse_program()
