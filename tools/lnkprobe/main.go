package main

import (
	"fmt"
	"os"

	"github.com/ngbcoder/NovaBar/internal/win"
)

func main() {
	for _, p := range os.Args[1:] {
		target, err := win.ResolveShortcut(p)
		if err != nil {
			fmt.Printf("%s\n  -> %v\n", p, err)
			continue
		}
		fmt.Printf("%s\n  -> %s\n", p, target)
	}
}
