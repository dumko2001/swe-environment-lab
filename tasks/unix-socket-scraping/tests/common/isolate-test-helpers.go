// Copy the trusted assertion-library closure and isolate its imports from submitted dependencies.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type pkg struct {
	ImportPath, Dir                               string
	Standard                                      bool
	GoFiles, CgoFiles, SFiles, HFiles, EmbedFiles []string
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(20)
	}
}
func rewrite(path string, mapping map[string]string) {
	data, err := os.ReadFile(path)
	die(err)
	text := string(data)
	for old, new := range mapping {
		text = strings.ReplaceAll(text, strconv.Quote(old), strconv.Quote(new))
	}
	die(os.WriteFile(path, []byte(text), 0644))
}
func main() {
	if len(os.Args) != 4 {
		die(fmt.Errorf("usage: isolate-test-helpers TRUSTED_GO REFERENCE_ROOT CANDIDATE_ROOT"))
	}
	goBinary, reference, candidate := os.Args[1], os.Args[2], os.Args[3]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBinary, "list", "-deps", "-json", "github.com/stretchr/testify/require")
	cmd.Dir = reference
	cmd.Env = append(os.Environ(), "GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=")
	data, err := cmd.Output()
	die(err)
	var packages []pkg
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for {
		var p pkg
		err = dec.Decode(&p)
		if err == io.EOF {
			break
		}
		die(err)
		if !p.Standard {
			packages = append(packages, p)
		}
	}
	modcmd := exec.CommandContext(ctx, goBinary, "list", "-m", "-f", "{{.Path}}")
	modcmd.Dir = reference
	modcmd.Env = cmd.Env
	module, err := modcmd.Output()
	die(err)
	nonce := make([]byte, 16)
	_, err = rand.Read(nonce)
	die(err)
	folder := "zz_verifier_support_" + hex.EncodeToString(nonce)
	prefix := strings.TrimSpace(string(module)) + "/" + folder
	mapping := map[string]string{}
	for _, p := range packages {
		hash := sha256.Sum256([]byte(p.ImportPath))
		mapping[p.ImportPath] = prefix + "/p" + hex.EncodeToString(hash[:8])
	}
	for _, p := range packages {
		dest := filepath.Join(candidate, folder, filepath.Base(mapping[p.ImportPath]))
		die(os.MkdirAll(dest, 0755))
		files := append(append(append(append(append([]string{}, p.GoFiles...), p.CgoFiles...), p.SFiles...), p.HFiles...), p.EmbedFiles...)
		for _, name := range files {
			data, err := os.ReadFile(filepath.Join(p.Dir, name))
			die(err)
			out := filepath.Join(dest, name)
			die(os.MkdirAll(filepath.Dir(out), 0755))
			die(os.WriteFile(out, data, 0644))
			if strings.HasSuffix(name, ".go") {
				rewrite(out, mapping)
			}
		}
	}
	// Every *_test.go here was restored from the trusted parent or injected by the verifier.
	die(filepath.WalkDir(candidate, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, "_test.go") {
			rewrite(path, mapping)
		}
		return nil
	}))
	// Production-facing helpers are not rewritten. These paths are test support only,
	// restored separately from the trusted parent before this helper is invoked.
	for _, relative := range []string{"promql/promqltest", "util/testutil", "util/teststorage"} {
		dir := filepath.Join(candidate, relative)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		die(filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".go") {
				rewrite(path, mapping)
			}
			return nil
		}))
	}
}
