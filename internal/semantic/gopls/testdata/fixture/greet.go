// Package fixture is a small, fixed Go source tree used by driver_query_test.go
// to verify Driver's query methods against known-good file:line results.
// fakegopls's fakeServer hardcodes its responses against these exact lines,
// independent of how Driver itself computes anything.
package fixture

// Greeter is implemented by anything that can produce a greeting.
type Greeter interface {
	Greet() string
}

// EnglishGreeter greets in English.
type EnglishGreeter struct{}

// Greet returns an English greeting for name.
func Greet(name string) string {
	return "Hello, " + name
}

// Caller invokes Greet.
func Caller() string {
	return Greet("world")
}
