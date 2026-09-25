// Command release verifies release-source trust for an immutable stable tag.
//
// Ported from FlanChanXwO/javdb-cli (MIT License, Copyright (c) 2026
// FlanChanXwO), reduced to the verify-source command: a release tag must
// resolve to a commit that is an ancestor of the protected default branch,
// so a tag cut from any other history cannot drive a release.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var stableTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type sourceTrust struct {
	Version string
	Commit  string
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("expected verify-source"))
	}
	switch os.Args[1] {
	case "verify-source":
		verifySourceCommand(os.Args[2:])
	default:
		fatal(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func verifySourceCommand(args []string) {
	set := flag.NewFlagSet("verify-source", flag.ExitOnError)
	repoRoot := set.String("repo-root", ".", "repository root")
	tag := set.String("tag", "", "immutable release tag")
	defaultBranch := set.String("default-branch", "", "default release branch")
	githubOutput := set.String("github-output", "", "optional GitHub Actions output file")
	_ = set.Parse(args)
	trust, err := verifySourceTrust(*repoRoot, *tag, *defaultBranch)
	if err != nil {
		fatal(err)
	}
	if *githubOutput != "" {
		if err := appendGitHubOutput(*githubOutput, map[string]string{
			"commit":  trust.Commit,
			"version": trust.Version,
		}); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("version=%s\ncommit=%s\n", trust.Version, trust.Commit)
}

// parseStableTag accepts exactly the tag grammar release.yml validates:
// strict SemVer with a mandatory v prefix. The reported version drops the v.
func parseStableTag(tag string) (string, error) {
	if !stableTagPattern.MatchString(tag) {
		return "", fmt.Errorf("release tag %q is not a strict stable SemVer tag with a v prefix", tag)
	}
	return strings.TrimPrefix(tag, "v"), nil
}

func verifySourceTrust(repoRoot, tag, defaultBranch string) (sourceTrust, error) {
	version, err := parseStableTag(tag)
	if err != nil {
		return sourceTrust{}, err
	}
	if strings.TrimSpace(defaultBranch) == "" {
		return sourceTrust{}, errors.New("default branch is required")
	}
	absoluteRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return sourceTrust{}, err
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return sourceTrust{}, err
	}
	if !info.IsDir() {
		return sourceTrust{}, fmt.Errorf("repository root is not a directory: %s", absoluteRoot)
	}
	if err := runGit(absoluteRoot, "show-ref", "--verify", "--quiet", "refs/tags/"+tag); err != nil {
		return sourceTrust{}, fmt.Errorf("release tag %q is unavailable: %w", tag, err)
	}
	commit, err := captureGit(absoluteRoot, "rev-parse", "--verify", tag+"^{commit}")
	if err != nil {
		return sourceTrust{}, fmt.Errorf("resolve release tag %q: %w", tag, err)
	}
	branchRef := "refs/remotes/origin/" + defaultBranch
	if err := runGit(absoluteRoot, "show-ref", "--verify", "--quiet", branchRef); err != nil {
		return sourceTrust{}, fmt.Errorf("default branch ref %q is unavailable: %w", branchRef, err)
	}
	if err := runGit(absoluteRoot, "merge-base", "--is-ancestor", commit, branchRef); err != nil {
		return sourceTrust{}, fmt.Errorf("release tag %q is not an ancestor of %s: %w", tag, branchRef, err)
	}
	return sourceTrust{Version: version, Commit: commit}, nil
}

// gitEnv returns the environment for spawning git against an explicit
// repository root. Git gives an inherited GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE
// precedence over `-C`, so a caller that exports GIT_* would silently retarget
// every repository operation. Strip them and pin the repo root explicitly.
func gitEnv(repoRoot string) []string {
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"GIT_DIR="+filepath.Join(repoRoot, ".git"),
		"GIT_WORK_TREE="+repoRoot,
		"GIT_INDEX_FILE="+filepath.Join(repoRoot, ".git", "index"),
	)
}

func runGit(repoRoot string, args ...string) error {
	command := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	command.Env = gitEnv(repoRoot)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func captureGit(repoRoot string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	command.Env = gitEnv(repoRoot)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("git %s returned empty output", strings.Join(args, " "))
	}
	return value, nil
}

func appendGitHubOutput(path string, values map[string]string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, key := range []string{"commit", "version"} {
		value, ok := values[key]
		if !ok {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("GitHub output %s contains a newline", key)
		}
		if _, err := fmt.Fprintf(file, "%s=%s\n", key, value); err != nil {
			return err
		}
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
