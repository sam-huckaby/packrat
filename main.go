package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

type Stash struct {
	Ref, Message, Created string
}

func main() {
	stashes, err := listStashes()
	if err != nil {
		log.Fatal(err)
	}
	if len(stashes) == 0 {
		fmt.Println("No stashes found.")
		return
	}

	fmt.Println("Git stashes:")
	for i, s := range stashes {
		fmt.Printf("[%d] %s - %s (%s)\n", i, s.Ref, s.Message, s.Created)
	}

	fmt.Print("\nSelect stash index to inspect (or 'q' to quit): ")
	var choice string
	fmt.Scanln(&choice)
	if choice == "q" {
		return
	}

	var index int
	fmt.Sscan(choice, &index)
	if index < 0 || index >= len(stashes) {
		fmt.Println("Invalid index.")
		return
	}

	ref := stashes[index].Ref
	diff, _ := showStash(ref)
	fmt.Printf("\n--- %s ---\n%s\n", ref, diff)

	fmt.Print("\nDelete this stash? (y/N): ")
	fmt.Scanln(&choice)
	if strings.ToLower(choice) == "y" {
		if err := dropStash(ref); err != nil {
			fmt.Println("Failed to delete:", err)
		} else {
			fmt.Println("Deleted", ref)
		}
	}
}

func listStashes() ([]Stash, error) {
	cmd := exec.Command("git", "stash", "list", "--pretty=format:%gd|%gs|%cr")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git stash list failed: %w", err)
	}

	var stashes []Stash
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "|", 3)
		if len(parts) == 3 {
			stashes = append(stashes, Stash{parts[0], parts[1], parts[2]})
		}
	}
	return stashes, scanner.Err()
}

func showStash(ref string) (string, error) {
	cmd := exec.Command("git", "stash", "show", "-p", ref)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func dropStash(ref string) error {
	cmd := exec.Command("git", "stash", "drop", ref)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

