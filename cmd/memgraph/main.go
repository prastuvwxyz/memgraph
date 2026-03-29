package main

var version = "dev" // injected by ldflags at build time

func main() {
	Execute(version)
}
