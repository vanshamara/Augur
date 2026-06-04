package main

import (
	"fmt"
	"os"

	"github.com/vanshamara/Augur/internal/scenario"
)

// main runs each product-promise scenario against scripted backends and prints a
// short report. It exits non-zero if any scenario does not hold. Run it with:
// go run ./cmd/demo
func main() {
	fmt.Println(scenario.ScopeNote)
	fmt.Println()

	allOK := true
	for _, result := range scenario.Run() {
		status := "PASS"
		if !result.OK {
			status = "FAIL"
			allOK = false
		}
		fmt.Printf("[%s] %s: %s\n", status, result.Name, result.Promise)
		fmt.Printf("       %s\n", result.Summary)
		for _, line := range result.Detail {
			fmt.Printf("       - %s\n", line)
		}
		fmt.Println()
	}

	if !allOK {
		fmt.Println("one or more scenarios did not hold")
		os.Exit(1)
	}
	fmt.Println("all six product promises held")
}
