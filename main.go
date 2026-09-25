// jat is a single-binary machine utility. Everything lives in ./cli; this
// file exists so `go install github.com/gostega/jat-util@latest` works.
package main

import "github.com/gostega/jat-util/cli"

func main() { cli.Main() }
