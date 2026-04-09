package main

var version = "v0.6.0" // injected by ldflags at build time

func main() {
	Execute(version)
}
