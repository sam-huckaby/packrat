package main

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
)

func main() {
	// Example: git status
	cmd := exec.Command("git", "status")

	// Capture both stdout and stderr
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	// Run the command
	err := cmd.Run()
	if err != nil {
		log.Fatalf("git command failed: %v\n%s", err, stderr.String())
	}

	fmt.Println("Output:")
	fmt.Println(out.String())
}
