package main

import (
	"flag"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bakito/extract-crd-api/internal/flags"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

var (
	excludeFlags flags.ArrayFlags
	module       string
	path         string
	target       string
	clearTarget  = false
	useGit       = false
)

func main() {
	flag.Var(&excludeFlags, "exclude", "Regex pattern for file excludes")
	flag.StringVar(&module, "module", "", "The go module to get the api files from")
	flag.StringVar(&path, "path", "", "The path within the module to the api files")
	flag.StringVar(&target, "target", "", "The target directory to copyFile the files to")
	flag.BoolVar(&clearTarget, "clear", false, "Clear target dir")
	flag.BoolVar(&useGit, "use-git", false, "Use git instead of go mod (of module is not proper versioned)")
	flag.Parse()

	if strings.TrimSpace(module) == "" {
		slog.Error("Flag must be defined", "flag", "module")
		return
	}
	if strings.TrimSpace(path) == "" {
		slog.Error("Flag must be defined", "flag", "path")
		return
	}
	if strings.TrimSpace(target) == "" {
		slog.Error("Flag must be defined", "flag", "target")
		return
	}

	var excludes []*regexp.Regexp
	for _, excludeFlag := range excludeFlags {
		excludes = append(excludes, regexp.MustCompile(excludeFlag))
	}

	tmp, err := os.MkdirTemp("", "extract-crd-api")
	if err != nil {
		slog.Error("Failed to create a temp dir", "error", err)
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	moduleRoot := tmp

	if useGit {
		slog.With("module", module, "tmp", tmp).Info("Cloning module")
		info := strings.Split(module, "@")
		r, err := git.PlainClone(tmp, false, &git.CloneOptions{
			URL:      "https://" + info[0],
			Progress: os.Stdout,
		})
		if err != nil {
			slog.Error("Failed to clone module", "error", err)
			return
		}
		w, err := r.Worktree()
		if err != nil {
			slog.Error("Failed to clone module", "error", err)
			return
		}
		if len(info) > 0 {
			err = w.Checkout(&git.CheckoutOptions{
				Branch: plumbing.NewTagReferenceName(info[1]),
			})
			if err != nil {
				slog.With("tag", info[1]).Error("Failed to checkout tag", "error", err)
				return
			}
		}
	} else {
		cmd := exec.Command("go", "mod", "download", module)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		cmd.Env = append(os.Environ(), "GOMODCACHE="+tmp)

		slog.With("module", module, "tmp", tmp).Info("Downloading")
		err = cmd.Run()
		if err != nil {
			slog.Error("Failed to download module", "error", err)
			return
		}
		cmd = exec.Command("chmod", "+w", "-R", tmp)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			slog.Error("Failed to download module", "error", err)
			return
		}
		moduleRoot = filepath.Join(tmp, module)
	}
	slog.Info("Module downloaded successfully!")

	apiPath := filepath.Join(moduleRoot, path)
	entries, err := os.ReadDir(filepath.Join(moduleRoot, path))
	if err != nil {
		slog.With("path", apiPath).Error("Failed to read files in api path", "error", err)
		return
	}

	if clearTarget {
		_ = os.RemoveAll(target)
	}
	err = os.MkdirAll(target, 0o755)
	if err != nil {
		slog.With("path", apiPath).Error("Failed to create api path dir", "error", err)
		return
	}

	for _, e := range entries {
		if keep(e.Name(), excludes) {
			err = copyFile(filepath.Join(apiPath, e.Name()), filepath.Join(target, e.Name()))
			if err != nil {
				slog.With("path", apiPath, "file", e.Name()).Error("FFailed to copyFile from api path", "error", err)
				return
			}
		}
	}
}

func keep(name string, excludes []*regexp.Regexp) bool {
	for _, exclude := range excludes {
		if exclude.MatchString(name) {
			return false
		}
	}
	return true
}

func copyFile(src, dst string) error {
	slog.With("from", src, "to", dst).Info("Copy file")
	// Read all content of src to data, may cause OOM for a large file.
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// Write data to dst
	return os.WriteFile(dst, data, 0o644)
}
