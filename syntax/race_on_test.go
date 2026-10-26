//go:build race

package syntax

// raceEnabled: the race detector slows the parser down by one to two orders
// of magnitude, so time bounds are relaxed accordingly.
const raceEnabled = true
