package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var validGitHubHandlePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
var noreplyEmailPattern = regexp.MustCompile(`^(?:[0-9]+\+)?([a-zA-Z0-9-]+)@users\.noreply\.github\.com$`)
var generateNotesAuthorPattern = regexp.MustCompile(`by @([a-zA-Z0-9-]+)`)

type assembleConfig struct {
	RepoRoot   string
	Tag        string
	Output     string
	GitHubRepo string
	Token      string
	APIBase    string
	HTTPClient *http.Client
}

func assembleNotesCommand(args []string) {
	set := flag.NewFlagSet("assemble-notes", flag.ExitOnError)
	repoRoot := set.String("repo-root", ".", "repository root")
	tag := set.String("tag", "", "immutable release tag (e.g. v0.9.0)")
	output := set.String("output", "", "optional output file for assembled release notes")
	githubRepo := set.String("github-repo", "", "optional GitHub owner/repo (defaults to GITHUB_REPOSITORY or GH_REPO)")
	token := set.String("token", "", "optional GitHub API token (defaults to GITHUB_TOKEN or GH_TOKEN)")
	apiBase := set.String("api-base", "https://api.github.com", "GitHub API base URL")
	_ = set.Parse(args)

	if *githubRepo == "" {
		*githubRepo = os.Getenv("GITHUB_REPOSITORY")
		if *githubRepo == "" {
			*githubRepo = os.Getenv("GH_REPO")
		}
	}
	if *token == "" {
		*token = os.Getenv("GITHUB_TOKEN")
		if *token == "" {
			*token = os.Getenv("GH_TOKEN")
		}
	}

	notes, err := assembleReleaseNotes(assembleConfig{
		RepoRoot:   *repoRoot,
		Tag:        *tag,
		Output:     *output,
		GitHubRepo: *githubRepo,
		Token:      *token,
		APIBase:    *apiBase,
	})
	if err != nil {
		fatal(err)
	}

	if *output != "" {
		dir := filepath.Dir(*output)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fatal(fmt.Errorf("create output directory %s: %w", dir, err))
		}
		if err := os.WriteFile(*output, []byte(notes), 0o644); err != nil {
			fatal(fmt.Errorf("write assembled notes %s: %w", *output, err))
		}
		fmt.Printf("assembled release notes written to %s (%d bytes)\n", *output, len(notes))
	} else {
		fmt.Print(notes)
	}
}

func assembleReleaseNotes(cfg assembleConfig) (string, error) {
	version, err := parseStableTag(cfg.Tag)
	if err != nil {
		return "", err
	}
	absoluteRoot, err := filepath.Abs(cfg.RepoRoot)
	if err != nil {
		return "", err
	}

	enPath := filepath.Join(absoluteRoot, "changelog", "v"+version, "en.md")
	zhPath := filepath.Join(absoluteRoot, "changelog", "v"+version, "zh-CN.md")

	enBytes, err := os.ReadFile(enPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", enPath, err)
	}
	zhBytes, err := os.ReadFile(zhPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", zhPath, err)
	}

	enContent := strings.TrimSpace(string(enBytes))
	zhContent := strings.TrimSpace(string(zhBytes))
	if enContent == "" {
		return "", fmt.Errorf("changelog file is empty: %s", enPath)
	}
	if zhContent == "" {
		return "", fmt.Errorf("changelog file is empty: %s", zhPath)
	}

	prevTag := findPreviousTag(absoluteRoot, cfg.Tag)
	contributors := fetchContributors(absoluteRoot, cfg, prevTag)

	var sb strings.Builder
	sb.WriteString(enContent)
	sb.WriteString("\n\n---\n\n")
	sb.WriteString(zhContent)

	if len(contributors) > 0 {
		sb.WriteString("\n\n---\n\n## Contributors / 贡献者\n\n")
		for _, c := range contributors {
			sb.WriteString(formatContributorLine(c))
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")

	return sb.String(), nil
}

func formatContributorLine(contributor string) string {
	contributor = strings.TrimSpace(contributor)
	contributor = strings.TrimPrefix(contributor, "@")
	if validGitHubHandlePattern.MatchString(contributor) {
		return fmt.Sprintf("- @%s", contributor)
	}
	return fmt.Sprintf("- %s", contributor)
}

func findPreviousTag(repoRoot, tag string) string {
	if prev, err := captureGit(repoRoot, "describe", "--tags", "--abbrev=0", tag+"^"); err == nil && prev != "" {
		return prev
	}
	output, err := captureGit(repoRoot, "tag", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return ""
	}
	lines := strings.Split(output, "\n")
	foundCurrent := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if t == tag {
			foundCurrent = true
			continue
		}
		if foundCurrent {
			return t
		}
	}
	return ""
}

func isBotLogin(login string) bool {
	lower := strings.ToLower(strings.TrimSpace(login))
	if lower == "" || lower == "web-flow" {
		return true
	}
	if strings.HasSuffix(lower, "[bot]") {
		return true
	}
	if lower == "github-actions" || lower == "dependabot" || lower == "renovate" {
		return true
	}
	return false
}

func fetchContributors(repoRoot string, cfg assembleConfig, prevTag string) []string {
	contributorMap := make(map[string]string) // normalized key -> display name

	addContributor := func(name string) {
		name = strings.TrimSpace(name)
		clean := strings.TrimPrefix(name, "@")
		if isBotLogin(clean) {
			return
		}
		key := strings.ToLower(clean)
		if _, exists := contributorMap[key]; !exists {
			contributorMap[key] = clean
		}
	}

	if cfg.GitHubRepo != "" {
		_ = fetchContributorsFromGitHub(cfg, prevTag, addContributor)
	}

	fetchContributorsFromGit(repoRoot, prevTag, cfg.Tag, addContributor)

	result := make([]string, 0, len(contributorMap))
	for _, display := range contributorMap {
		result = append(result, display)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i]) < strings.ToLower(result[j])
	})
	return result
}

func fetchContributorsFromGitHub(cfg assembleConfig, prevTag string, add func(string)) error {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	apiBase := strings.TrimRight(cfg.APIBase, "/")

	var compareURL string
	if prevTag != "" {
		compareURL = fmt.Sprintf("%s/repos/%s/compare/%s...%s", apiBase, cfg.GitHubRepo, prevTag, cfg.Tag)
	} else {
		compareURL = fmt.Sprintf("%s/repos/%s/commits?sha=%s", apiBase, cfg.GitHubRepo, cfg.Tag)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, compareURL, nil)
	if err == nil {
		req.Header.Set("User-Agent", "nitter-cli-release-notes")
		req.Header.Set("Accept", "application/vnd.github+json")
		if cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.Token)
		}

		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				if prevTag != "" {
					var comp struct {
						Commits []struct {
							Author *struct {
								Login string `json:"login"`
							} `json:"author"`
						} `json:"commits"`
					}
					if json.Unmarshal(bodyBytes, &comp) == nil {
						for _, c := range comp.Commits {
							if c.Author != nil {
								add(c.Author.Login)
							}
						}
					}
				} else {
					var commits []struct {
						Author *struct {
							Login string `json:"login"`
						} `json:"author"`
					}
					if json.Unmarshal(bodyBytes, &commits) == nil {
						for _, c := range commits {
							if c.Author != nil {
								add(c.Author.Login)
							}
						}
					}
				}
			}
		}
	}

	if prevTag != "" {
		genURL := fmt.Sprintf("%s/repos/%s/releases/generate-notes", apiBase, cfg.GitHubRepo)
		payload, _ := json.Marshal(map[string]string{
			"tag_name":          cfg.Tag,
			"previous_tag_name": prevTag,
		})
		genReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, genURL, bytes.NewReader(payload))
		if err == nil {
			genReq.Header.Set("User-Agent", "nitter-cli-release-notes")
			genReq.Header.Set("Accept", "application/vnd.github+json")
			if cfg.Token != "" {
				genReq.Header.Set("Authorization", "Bearer "+cfg.Token)
			}
			if genResp, err := client.Do(genReq); err == nil {
				defer genResp.Body.Close()
				if genResp.StatusCode == http.StatusOK {
					var genData struct {
						Body string `json:"body"`
					}
					if json.NewDecoder(genResp.Body).Decode(&genData) == nil {
						matches := generateNotesAuthorPattern.FindAllStringSubmatch(genData.Body, -1)
						for _, m := range matches {
							if len(m) > 1 {
								add(m[1])
							}
						}
					}
				}
			}
		}
	}

	return nil
}

func fetchContributorsFromGit(repoRoot, prevTag, tag string, add func(string)) {
	var commitRange string
	if prevTag != "" {
		commitRange = prevTag + ".." + tag
	} else {
		commitRange = tag
	}

	output, err := captureGit(repoRoot, "log", commitRange, "--format=%an|%ae")
	if err == nil {
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "|", 2)
			name := strings.TrimSpace(parts[0])
			email := ""
			if len(parts) > 1 {
				email = strings.TrimSpace(parts[1])
			}
			if m := noreplyEmailPattern.FindStringSubmatch(email); len(m) > 1 {
				add(m[1])
				continue
			}
			if name != "" {
				add(name)
			}
		}
	}

	trailers, err := captureGit(repoRoot, "log", commitRange, "--format=%(trailers:key=Co-authored-by,valueonly=true)")
	if err == nil {
		for _, line := range strings.Split(trailers, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Format: Name <email>
			if idx := strings.Index(line, "<"); idx != -1 {
				name := strings.TrimSpace(line[:idx])
				email := strings.Trim(line[idx:], "<> ")
				if m := noreplyEmailPattern.FindStringSubmatch(email); len(m) > 1 {
					add(m[1])
					continue
				}
				if name != "" {
					add(name)
				}
			}
		}
	}
}
