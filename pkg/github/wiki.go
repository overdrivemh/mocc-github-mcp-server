package github

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	ghcontext "github.com/github/github-mcp-server/pkg/context"
	"github.com/github/github-mcp-server/pkg/ifc"
	"github.com/github/github-mcp-server/pkg/inventory"
	"github.com/github/github-mcp-server/pkg/scopes"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/github/github-mcp-server/pkg/utils"
	gogithub "github.com/google/go-github/v89/github"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var ToolsetMetadataWiki = inventory.ToolsetMetadata{
	ID:          "wiki",
	Description: "GitHub Wiki page tools",
	Icon:        "book",
}

const (
	wikiMaxPages       = 50
	wikiMaxPageBytes   = 1 << 20
	wikiMaxTotalBytes  = 5 << 20
	wikiCommitMessage  = "Update GitHub Wiki via MCP"
	wikiCommitterName  = "GitHub MCP Wiki"
	wikiCommitterEmail = "github-mcp-wiki@users.noreply.github.com"
)

type wikiRepository struct {
	Owner     string
	Repo      string
	RemoteURL string
}

type wikiPageInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type wikiPageResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Head    string `json:"head"`
	Branch  string `json:"branch"`
}

type wikiHeadResult struct {
	Head   string `json:"head"`
	Branch string `json:"branch"`
}

type wikiPagesResult struct {
	Pages  []string `json:"pages"`
	Head   string   `json:"head"`
	Branch string   `json:"branch"`
}

type wikiPublishResult struct {
	Changed      bool     `json:"changed"`
	PreviousHead string   `json:"previous_head"`
	Head         string   `json:"head"`
	Branch       string   `json:"branch"`
	Pages        []string `json:"pages"`
}

func wikiRepositorySchemaProperties() map[string]*jsonschema.Schema {
	return map[string]*jsonschema.Schema{
		"owner": {
			Type:        "string",
			Description: "Repository owner",
		},
		"repo": {
			Type:        "string",
			Description: "Repository name whose GitHub Wiki should be accessed",
		},
	}
}

func wikiRepositoryInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		Properties:           wikiRepositorySchemaProperties(),
		Required:             []string{"owner", "repo"},
	}
}

// WikiGetHead returns the exact Git commit backing a repository Wiki.
func WikiGetHead(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataWiki,
		mcp.Tool{
			Name:        "wiki_get_head",
			Description: t("TOOL_WIKI_GET_HEAD_DESCRIPTION", "Get the exact Git commit and branch backing a GitHub Wiki"),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_WIKI_GET_HEAD_USER_TITLE", "Get Wiki head"),
				ReadOnlyHint: true,
			},
			InputSchema: wikiRepositoryInputSchema(),
		},
		scopes.PublicRead(scopes.Repo),
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			repository, client, err := resolveWikiRepository(ctx, deps, args)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			head, branch, err := wikiRemoteHead(ctx, repository.RemoteURL, wikiGitToken(ctx))
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			result, err := wikiJSONResult(wikiHeadResult{Head: head, Branch: branch})
			if err != nil {
				return nil, nil, err
			}
			return attachRepoVisibilityIFCLabel(ctx, deps, client, repository.Owner, repository.Repo, result, ifc.LabelCommitContents), nil, nil
		},
	)
}

// WikiListPages lists the root Markdown pages in a repository Wiki.
func WikiListPages(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataWiki,
		mcp.Tool{
			Name:        "wiki_list_pages",
			Description: t("TOOL_WIKI_LIST_PAGES_DESCRIPTION", "List Markdown pages in a GitHub Wiki and return the exact Wiki head"),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_WIKI_LIST_PAGES_USER_TITLE", "List Wiki pages"),
				ReadOnlyHint: true,
			},
			InputSchema: wikiRepositoryInputSchema(),
		},
		scopes.PublicRead(scopes.Repo),
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			repository, client, err := resolveWikiRepository(ctx, deps, args)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			checkout, head, branch, cleanup, err := wikiClone(ctx, repository.RemoteURL, wikiGitToken(ctx))
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			defer cleanup()

			entries, err := os.ReadDir(checkout)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to read Wiki checkout: %v", err)), nil, nil
			}
			pages := make([]string, 0, len(entries))
			for _, entry := range entries {
				if entry.Type().IsRegular() && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
					pages = append(pages, entry.Name())
				}
			}
			sort.Strings(pages)

			result, err := wikiJSONResult(wikiPagesResult{Pages: pages, Head: head, Branch: branch})
			if err != nil {
				return nil, nil, err
			}
			return attachRepoVisibilityIFCLabel(ctx, deps, client, repository.Owner, repository.Repo, result, ifc.LabelCommitContents), nil, nil
		},
	)
}

// WikiGetPage reads one Markdown page from a repository Wiki.
func WikiGetPage(t translations.TranslationHelperFunc) inventory.ServerTool {
	properties := wikiRepositorySchemaProperties()
	properties["path"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Wiki page filename, for example Home.md or Start-Here.md. Only root Markdown pages are supported.",
	}
	return NewTool(
		ToolsetMetadataWiki,
		mcp.Tool{
			Name:        "wiki_get_page",
			Description: t("TOOL_WIKI_GET_PAGE_DESCRIPTION", "Read one Markdown page from a GitHub Wiki and return the exact Wiki head"),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_WIKI_GET_PAGE_USER_TITLE", "Get Wiki page"),
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{
				Type:                 "object",
				AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
				Properties:           properties,
				Required:             []string{"owner", "repo", "path"},
			},
		},
		scopes.PublicRead(scopes.Repo),
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			path, err := RequiredParam[string](args, "path")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			if err := validateWikiPagePath(path); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			repository, client, err := resolveWikiRepository(ctx, deps, args)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			checkout, head, branch, cleanup, err := wikiClone(ctx, repository.RemoteURL, wikiGitToken(ctx))
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			defer cleanup()

			content, err := os.ReadFile(filepath.Join(checkout, path))
			if err != nil {
				if os.IsNotExist(err) {
					return utils.NewToolResultError(fmt.Sprintf("Wiki page %q does not exist", path)), nil, nil
				}
				return utils.NewToolResultError(fmt.Sprintf("failed to read Wiki page %q: %v", path, err)), nil, nil
			}
			if len(content) > wikiMaxPageBytes {
				return utils.NewToolResultError(fmt.Sprintf("Wiki page %q exceeds the %d-byte tool limit", path, wikiMaxPageBytes)), nil, nil
			}

			result, err := wikiJSONResult(wikiPageResult{Path: path, Content: string(content), Head: head, Branch: branch})
			if err != nil {
				return nil, nil, err
			}
			return attachRepoVisibilityIFCLabel(ctx, deps, client, repository.Owner, repository.Repo, result, ifc.LabelCommitContents), nil, nil
		},
	)
}

// WikiPublishPages atomically updates one or more Wiki Markdown pages in one Git commit.
func WikiPublishPages(t translations.TranslationHelperFunc) inventory.ServerTool {
	properties := wikiRepositorySchemaProperties()
	properties["expected_head"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Exact 40-character Wiki Git commit expected before the update. The write fails closed if the Wiki has moved.",
	}
	properties["pages"] = &jsonschema.Schema{
		Type:        "array",
		Description: "Wiki Markdown pages to create or replace atomically in one commit",
		Items: &jsonschema.Schema{
			Type:                 "object",
			AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
			Properties: map[string]*jsonschema.Schema{
				"path": {
					Type:        "string",
					Description: "Root Wiki Markdown filename, for example Home.md or _Sidebar.md",
				},
				"content": {
					Type:        "string",
					Description: "Complete Markdown content for the page",
				},
			},
			Required: []string{"path", "content"},
		},
	}

	return NewTool(
		ToolsetMetadataWiki,
		mcp.Tool{
			Name:        "wiki_publish_pages",
			Description: t("TOOL_WIKI_PUBLISH_PAGES_DESCRIPTION", "Create or replace GitHub Wiki Markdown pages atomically using expected-head concurrency protection and exact remote-head verification"),
			Annotations: &mcp.ToolAnnotations{
				Title:           t("TOOL_WIKI_PUBLISH_PAGES_USER_TITLE", "Publish Wiki pages"),
				ReadOnlyHint:    false,
				DestructiveHint: jsonschema.Ptr(false),
			},
			InputSchema: &jsonschema.Schema{
				Type:                 "object",
				AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
				Properties:           properties,
				Required:             []string{"owner", "repo", "expected_head", "pages"},
			},
		},
		scopes.RequireAll(scopes.Repo),
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			expectedHead, err := RequiredParam[string](args, "expected_head")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			if err := validateWikiHead(expectedHead); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			pages, err := parseWikiPages(args)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			repository, _, err := resolveWikiRepository(ctx, deps, args)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			token := wikiGitToken(ctx)
			if token == "" {
				return utils.NewToolResultError("Wiki publication requires an authenticated GitHub token with repo access"), nil, nil
			}

			remoteHead, remoteBranch, err := wikiRemoteHead(ctx, repository.RemoteURL, token)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			if remoteHead != expectedHead {
				return utils.NewToolResultError(fmt.Sprintf("Wiki head moved: expected %s, current %s", expectedHead, remoteHead)), nil, nil
			}

			checkout, clonedHead, branch, cleanup, err := wikiClone(ctx, repository.RemoteURL, token)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			defer cleanup()
			if clonedHead != expectedHead {
				return utils.NewToolResultError(fmt.Sprintf("Wiki head changed during checkout: expected %s, cloned %s", expectedHead, clonedHead)), nil, nil
			}
			if branch != remoteBranch {
				return utils.NewToolResultError(fmt.Sprintf("Wiki branch changed during checkout: expected %s, cloned %s", remoteBranch, branch)), nil, nil
			}

			pagePaths := make([]string, 0, len(pages))
			for _, page := range pages {
				if err := os.WriteFile(filepath.Join(checkout, page.Path), []byte(page.Content), 0o600); err != nil {
					return utils.NewToolResultError(fmt.Sprintf("failed to write Wiki page %q: %v", page.Path, err)), nil, nil
				}
				pagePaths = append(pagePaths, page.Path)
			}
			sort.Strings(pagePaths)

			statusArgs := append([]string{"status", "--porcelain", "--"}, pagePaths...)
			status, err := runWikiGit(ctx, checkout, repository.RemoteURL, token, statusArgs...)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			if strings.TrimSpace(status) == "" {
				result, err := wikiJSONResult(wikiPublishResult{
					Changed: false, PreviousHead: expectedHead, Head: expectedHead, Branch: branch, Pages: pagePaths,
				})
				if err != nil {
					return nil, nil, err
				}
				return result, nil, nil
			}

			addArgs := append([]string{"add", "--"}, pagePaths...)
			if _, err := runWikiGit(ctx, checkout, repository.RemoteURL, token, addArgs...); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			if _, err := runWikiGit(ctx, checkout, repository.RemoteURL, token,
				"-c", "user.name="+wikiCommitterName,
				"-c", "user.email="+wikiCommitterEmail,
				"commit", "--quiet", "-m", wikiCommitMessage,
			); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			localHead, err := wikiLocalHead(ctx, checkout, repository.RemoteURL, token)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			prePushHead, prePushBranch, err := wikiRemoteHead(ctx, repository.RemoteURL, token)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			if prePushHead != expectedHead || prePushBranch != branch {
				return utils.NewToolResultError(fmt.Sprintf("Wiki changed before push: expected %s on %s, current %s on %s", expectedHead, branch, prePushHead, prePushBranch)), nil, nil
			}

			if _, err := runWikiGit(ctx, checkout, repository.RemoteURL, token, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			verifiedHead, verifiedBranch, err := wikiRemoteHead(ctx, repository.RemoteURL, token)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			if verifiedHead != localHead || verifiedBranch != branch {
				return utils.NewToolResultError(fmt.Sprintf("Wiki remote-head verification failed: local %s on %s, remote %s on %s", localHead, branch, verifiedHead, verifiedBranch)), nil, nil
			}

			result, err := wikiJSONResult(wikiPublishResult{
				Changed: true, PreviousHead: expectedHead, Head: verifiedHead, Branch: branch, Pages: pagePaths,
			})
			if err != nil {
				return nil, nil, err
			}
			return result, nil, nil
		},
	)
}

func resolveWikiRepository(ctx context.Context, deps ToolDependencies, args map[string]any) (wikiRepository, *gogithub.Client, error) {
	owner, err := RequiredParam[string](args, "owner")
	if err != nil {
		return wikiRepository{}, nil, err
	}
	repo, err := RequiredParam[string](args, "repo")
	if err != nil {
		return wikiRepository{}, nil, err
	}
	client, err := deps.GetClient(ctx)
	if err != nil {
		return wikiRepository{}, nil, fmt.Errorf("failed to get GitHub client: %w", err)
	}
	repository, _, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return wikiRepository{}, nil, fmt.Errorf("failed to resolve repository %s/%s: %w", owner, repo, err)
	}
	if !repository.GetHasWiki() {
		return wikiRepository{}, nil, fmt.Errorf("github Wiki is not enabled for %s/%s", owner, repo)
	}
	htmlURL := strings.TrimSuffix(repository.GetHTMLURL(), "/")
	parsed, err := url.Parse(htmlURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return wikiRepository{}, nil, fmt.Errorf("repository %s/%s returned an invalid HTML URL", owner, repo)
	}
	if parsed.Scheme != "https" {
		return wikiRepository{}, nil, fmt.Errorf("wiki Git transport requires HTTPS repository URLs")
	}
	return wikiRepository{Owner: owner, Repo: repo, RemoteURL: htmlURL + ".wiki.git"}, client, nil
}

func parseWikiPages(args map[string]any) ([]wikiPageInput, error) {
	raw, ok := args["pages"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("missing required parameter: pages")
	}

	var values []any
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []map[string]any:
		values = make([]any, len(typed))
		for i := range typed {
			values[i] = typed[i]
		}
	default:
		return nil, fmt.Errorf("parameter pages must be an array")
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("parameter pages must contain at least one page")
	}
	if len(values) > wikiMaxPages {
		return nil, fmt.Errorf("parameter pages exceeds the %d-page transaction limit", wikiMaxPages)
	}

	pages := make([]wikiPageInput, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	totalBytes := 0
	for i, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("pages[%d] must be an object", i)
		}
		path, ok := item["path"].(string)
		if !ok {
			return nil, fmt.Errorf("pages[%d].path must be a string", i)
		}
		content, ok := item["content"].(string)
		if !ok {
			return nil, fmt.Errorf("pages[%d].content must be a string", i)
		}
		if err := validateWikiPagePath(path); err != nil {
			return nil, fmt.Errorf("pages[%d]: %w", i, err)
		}
		if _, duplicate := seen[path]; duplicate {
			return nil, fmt.Errorf("duplicate Wiki page path %q", path)
		}
		seen[path] = struct{}{}
		if len(content) > wikiMaxPageBytes {
			return nil, fmt.Errorf("wiki page %q exceeds the %d-byte page limit", path, wikiMaxPageBytes)
		}
		totalBytes += len(content)
		if totalBytes > wikiMaxTotalBytes {
			return nil, fmt.Errorf("wiki publication exceeds the %d-byte transaction limit", wikiMaxTotalBytes)
		}
		pages = append(pages, wikiPageInput{Path: path, Content: content})
	}
	return pages, nil
}

func validateWikiPagePath(path string) error {
	if path == "" {
		return fmt.Errorf("wiki page path must not be empty")
	}
	if len(path) > 255 {
		return fmt.Errorf("wiki page path exceeds 255 bytes")
	}
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		return fmt.Errorf("wiki page path %q must end in .md", path)
	}
	if path == ".md" || strings.HasPrefix(path, ".") || strings.HasPrefix(path, "-") {
		return fmt.Errorf("wiki page path %q has a prohibited prefix", path)
	}
	if strings.ContainsAny(path, `/\\`) || strings.Contains(path, "..") {
		return fmt.Errorf("wiki page path %q must be a root filename without traversal", path)
	}
	for _, r := range path {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("wiki page path %q contains unsupported character %q", path, r)
	}
	return nil
}

func validateWikiHead(head string) error {
	if len(head) != 40 {
		return fmt.Errorf("expected_head must be an exact 40-character Git SHA-1")
	}
	if _, err := hex.DecodeString(head); err != nil {
		return fmt.Errorf("expected_head must be an exact hexadecimal Git SHA-1")
	}
	return nil
}

func wikiGitToken(ctx context.Context) string {
	if tokenInfo, ok := ghcontext.GetTokenInfo(ctx); ok && tokenInfo != nil && tokenInfo.Token != "" {
		return tokenInfo.Token
	}
	for _, name := range []string{"GITHUB_PERSONAL_ACCESS_TOKEN", "GH_TOKEN"} {
		if token := os.Getenv(name); token != "" {
			return token
		}
	}
	return ""
}

func wikiRemoteHead(ctx context.Context, remoteURL, token string) (string, string, error) {
	output, err := runWikiGit(ctx, "", remoteURL, token, "ls-remote", "--symref", remoteURL, "HEAD")
	if err != nil {
		return "", "", wikiAccessError(err)
	}
	return parseWikiRemoteHead(output)
}

func parseWikiRemoteHead(output string) (string, string, error) {
	var head, branch string
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "ref: refs/heads/") && strings.HasSuffix(line, "\tHEAD") {
			branch = strings.TrimSuffix(strings.TrimPrefix(line, "ref: refs/heads/"), "\tHEAD")
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "HEAD" && len(fields[0]) == 40 {
			head = fields[0]
		}
	}
	if err := validateWikiHead(head); err != nil {
		return "", "", fmt.Errorf("failed to resolve Wiki remote head")
	}
	if branch == "" || strings.ContainsAny(branch, " \t\r\n") {
		return "", "", fmt.Errorf("failed to resolve Wiki default branch")
	}
	return head, branch, nil
}

func wikiClone(ctx context.Context, remoteURL, token string) (string, string, string, func(), error) {
	root, err := os.MkdirTemp("", "github-mcp-wiki-")
	if err != nil {
		return "", "", "", func() {}, fmt.Errorf("failed to create temporary Wiki checkout: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	checkout := filepath.Join(root, "wiki")
	if _, err := runWikiGit(ctx, "", remoteURL, token, "clone", "--quiet", "--no-tags", "--depth", "1", remoteURL, checkout); err != nil {
		cleanup()
		return "", "", "", func() {}, wikiAccessError(err)
	}
	head, err := wikiLocalHead(ctx, checkout, remoteURL, token)
	if err != nil {
		cleanup()
		return "", "", "", func() {}, err
	}
	branchOut, err := runWikiGit(ctx, checkout, remoteURL, token, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		cleanup()
		return "", "", "", func() {}, fmt.Errorf("failed to resolve Wiki checkout branch: %w", err)
	}
	branch := strings.TrimSpace(branchOut)
	if branch == "" || strings.ContainsAny(branch, " \t\r\n") {
		cleanup()
		return "", "", "", func() {}, fmt.Errorf("wiki checkout returned an invalid branch")
	}
	return checkout, head, branch, cleanup, nil
}

func wikiLocalHead(ctx context.Context, checkout, remoteURL, token string) (string, error) {
	output, err := runWikiGit(ctx, checkout, remoteURL, token, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to resolve Wiki checkout head: %w", err)
	}
	head := strings.TrimSpace(output)
	if err := validateWikiHead(head); err != nil {
		return "", fmt.Errorf("wiki checkout returned an invalid head")
	}
	return head, nil
}

func runWikiGit(ctx context.Context, dir, remoteURL, token string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("github Wiki tools require git in the MCP server runtime")
	}
	env, authValue, err := wikiGitProcessEnv(remoteURL, token)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		operation := "git"
		if len(args) > 0 {
			operation = "git " + args[0]
		}
		safe := sanitizeWikiGitOutput(string(output), token, authValue)
		if strings.TrimSpace(safe) == "" {
			return "", fmt.Errorf("%s failed: %w", operation, err)
		}
		return "", fmt.Errorf("%s failed: %s", operation, strings.TrimSpace(safe))
	}
	return string(output), nil
}

func wikiGitProcessEnv(remoteURL, token string) ([]string, string, error) {
	parsed, err := url.Parse(remoteURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, "", fmt.Errorf("invalid Wiki Git remote")
	}

	blockedPrefixes := []string{
		"GITHUB_PERSONAL_ACCESS_TOKEN=",
		"GITHUB_TOKEN=",
		"GH_TOKEN=",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_TERMINAL_PROMPT=",
		"GCM_INTERACTIVE=",
		"GIT_CONFIG_GLOBAL=",
		"GIT_CONFIG_NOSYSTEM=",
		"GIT_CONFIG_COUNT=",
		"GIT_CONFIG_KEY_",
		"GIT_CONFIG_VALUE_",
	}
	env := make([]string, 0, len(os.Environ())+9)
	for _, value := range os.Environ() {
		blocked := false
		for _, prefix := range blockedPrefixes {
			if strings.HasPrefix(value, prefix) {
				blocked = true
				break
			}
		}
		if !blocked {
			env = append(env, value)
		}
	}
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)

	if token == "" {
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=credential.helper",
			"GIT_CONFIG_VALUE_0=",
		)
		return env, "", nil
	}

	authValue := "AUTHORIZATION: basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	env = append(env,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http."+parsed.Scheme+"://"+parsed.Host+"/.extraheader",
		"GIT_CONFIG_VALUE_0="+authValue,
		"GIT_CONFIG_KEY_1=credential.helper",
		"GIT_CONFIG_VALUE_1=",
	)
	return env, authValue, nil
}

func sanitizeWikiGitOutput(output, token, authValue string) string {
	if token != "" {
		output = strings.ReplaceAll(output, token, "[REDACTED]")
	}
	if authValue != "" {
		output = strings.ReplaceAll(output, authValue, "[REDACTED]")
	}
	return output
}

func wikiAccessError(err error) error {
	message := err.Error()
	lower := strings.ToLower(message)
	if strings.Contains(lower, "repository not found") || strings.Contains(lower, "not found") {
		return fmt.Errorf("failed to access Wiki Git repository; ensure the Wiki is enabled and at least one page has been created on GitHub")
	}
	return err
}

func wikiJSONResult(value any) (*mcp.CallToolResult, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Wiki result: %w", err)
	}
	return utils.NewToolResultText(string(payload)), nil
}
