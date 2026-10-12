// Keep verifier-injected source out of application embed assets during compilation.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type embedConfig struct {
	Patterns map[string][]string
	Files    map[string]string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	args := append([]string{}, os.Args[1:]...)
	if len(args) == 0 {
		return fmt.Errorf("compiler tool argument missing")
	}
	private := map[string]bool{}
	for _, p := range filepath.SplitList(os.Getenv("MICRO1_PRIVATE_SOURCES")) {
		if p != "" {
			private[filepath.Clean(p)] = true
		}
	}
	for i, arg := range args {
		if arg != "-embedcfg" || i+1 == len(args) || len(private) == 0 {
			continue
		}
		data, err := os.ReadFile(args[i+1])
		if err != nil {
			return err
		}
		var config embedConfig
		if err = json.Unmarshal(data, &config); err != nil {
			return err
		}
		removed := map[string]bool{}
		for name, path := range config.Files {
			if private[filepath.Clean(path)] {
				removed[name] = true
				delete(config.Files, name)
			}
		}
		if len(removed) == 0 {
			continue
		}
		for pattern, names := range config.Patterns {
			kept := []string{}
			for _, name := range names {
				if !removed[name] {
					kept = append(kept, name)
				}
			}
			config.Patterns[pattern] = kept
		}
		output, err := os.CreateTemp("", "verifier-embed-*.json")
		if err != nil {
			return err
		}
		if err = json.NewEncoder(output).Encode(config); err != nil {
			output.Close()
			return err
		}
		if err = output.Close(); err != nil {
			return err
		}
		args[i+1] = output.Name()
	}
	// Tool paths are supplied by the trusted Go command, not shell interpreted.
	if !strings.ContainsRune(args[0], os.PathSeparator) {
		return fmt.Errorf("absolute compiler tool path required")
	}
	return syscall.Exec(args[0], args, os.Environ())
}
