module example.org/netsec/q0300

go 1.24.0

require golang.org/x/net v0.0.0

require golang.org/x/text v0.0.0 // indirect

replace golang.org/x/net => ./third_party/golang.org/x/net

replace golang.org/x/text => ./third_party/golang.org/x/text
