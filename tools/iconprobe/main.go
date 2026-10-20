package main

import (
	"fmt"
	"os"

	"github.com/ngbcoder/NovaBar/internal/win"
)

func main() {
	for _, p := range os.Args[1:] {
		lines, err := win.DebugIconResources(p)
		if err != nil {
			fmt.Println(p, err)
			continue
		}
		fmt.Println("==", p)
		for _, l := range lines {
			fmt.Println("  ", l)
		}
	}
}
