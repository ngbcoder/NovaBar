package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ngbcoder/NovaBar/internal/win"
)

func main() {
	targets := os.Args[1:]
	for _, t := range targets {
		data, err := win.ExtractIconPNG(t)
		if err != nil {
			fmt.Println(t, "->", err)
			continue
		}
		out := filepath.Join(os.TempDir(), "icon_"+filepath.Base(t)+".png")
		os.WriteFile(out, data, 0o644)
		fmt.Println(t, "->", out, len(data), "bytes")
	}
}
