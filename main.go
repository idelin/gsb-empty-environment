package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type cliInput struct {
	Alphabet           string         `json:"alphabet"`
	MaxPathLength      int            `json:"max_path_length"`
	Role               string         `json:"role"`
	RestrictedBackends []string       `json:"restricted_backends"`
	GatewayRoutes      []GatewayRoute `json:"gateway_routes"`
	OriginRoutes       []OriginRoute  `json:"origin_routes"`
}

type cliOutput struct {
	Vulnerable     bool   `json:"vulnerable"`
	Path           string `json:"path,omitempty"`
	Length         int    `json:"length,omitempty"`
	GatewayRoute   int    `json:"gateway_route,omitempty"`
	OriginRoute    int    `json:"origin_route,omitempty"`
	GatewayRewrite string `json:"gateway_rewrite,omitempty"`
	OriginRewrite  string `json:"origin_rewrite,omitempty"`
	Backend        string `json:"backend,omitempty"`
}

func main() {
	inputPath := flag.String("file", "", "path to a JSON input file; defaults to standard input")
	flag.Parse()

	inputBytes, err := readInput(*inputPath)
	if err != nil {
		writeError(err)
		os.Exit(1)
	}

	var input cliInput
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		writeError(err)
		os.Exit(1)
	}

	restricted := make(map[string]bool, len(input.RestrictedBackends))
	for _, backend := range input.RestrictedBackends {
		restricted[backend] = true
	}

	path, trace, err := FindShortestPath(
		input.Alphabet,
		input.MaxPathLength,
		input.GatewayRoutes,
		input.OriginRoutes,
		input.Role,
		restricted,
	)
	if err != nil {
		writeError(err)
		os.Exit(1)
	}

	output := cliOutput{Vulnerable: trace != nil}
	if trace != nil {
		output.Path = path
		output.Length = trace.Length
		output.GatewayRoute = trace.GatewayRoute
		output.OriginRoute = trace.OriginRoute
		output.GatewayRewrite = trace.GatewayRewrite
		output.OriginRewrite = trace.OriginRewrite
		output.Backend = trace.Backend
	}

	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		os.Exit(1)
	}
}

func readInput(path string) ([]byte, error) {
	if path == "" {
		return os.ReadFile(os.Stdin.Name())
	}
	return os.ReadFile(path)
}

func writeError(err error) {
	_ = json.NewEncoder(os.Stderr).Encode(struct {
		Error string `json:"error"`
	}{Error: err.Error()})
	fmt.Fprintln(os.Stderr)
}
