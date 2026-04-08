package main

var version = "v0.5.0" // injected by ldflags at build time

func main() {
	Execute(version)
}
