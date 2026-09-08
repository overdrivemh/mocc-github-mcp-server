package github

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWikiPagePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "home", path: "Home.md"},
		{name: "hyphenated", path: "Start-Here.md"},
		{name: "sidebar", path: "_Sidebar.md"},
		{name: "unicode", path: "Arquitetura-Visao.md"},
		{name: "empty", path: "", wantErr: "must not be empty"},
		{name: "traversal", path: "../Home.md", wantErr: "prohibited prefix"},
		{name: "directory", path: "docs/Home.md", wantErr: "root filename"},
		{name: "backslash", path: `docs\\Home.md`, wantErr: "root filename"},
		{name: "hidden", path: ".Home.md", wantErr: "prohibited prefix"},
		{name: "dash prefix", path: "-Home.md", wantErr: "prohibited prefix"},
		{name: "wrong extension", path: "Home.txt", wantErr: "must end in .md"},
		{name: "space", path: "Home Page.md", wantErr: "unsupported character"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWikiPagePath(tt.path)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateWikiHead(t *testing.T) {
	require.NoError(t, validateWikiHead(strings.Repeat("a", 40)))
	assert.Error(t, validateWikiHead(strings.Repeat("a", 39)))
	assert.Error(t, validateWikiHead(strings.Repeat("z", 40)))
}

func TestParseWikiRemoteHead(t *testing.T) {
	sha := strings.Repeat("1", 40)
	head, branch, err := parseWikiRemoteHead("ref: refs/heads/master\tHEAD\n" + sha + "\tHEAD\n")
	require.NoError(t, err)
	assert.Equal(t, sha, head)
	assert.Equal(t, "master", branch)

	_, _, err = parseWikiRemoteHead(sha + "\tHEAD\n")
	assert.Error(t, err)
}

func TestParseWikiPages(t *testing.T) {
	pages, err := parseWikiPages(map[string]any{
		"pages": []any{
			map[string]any{"path": "Home.md", "content": "# Home\n"},
			map[string]any{"path": "_Sidebar.md", "content": "[Home](Home)\n"},
		},
	})
	require.NoError(t, err)
	require.Len(t, pages, 2)
	assert.Equal(t, "Home.md", pages[0].Path)

	_, err = parseWikiPages(map[string]any{
		"pages": []any{
			map[string]any{"path": "Home.md", "content": "one"},
			map[string]any{"path": "Home.md", "content": "two"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestWikiGitProcessEnvDoesNotPersistRawToken(t *testing.T) {
	token := "ghp_test_secret_token"
	env, authValue, err := wikiGitProcessEnv("https://github.com/owner/repo.wiki.git", token)
	require.NoError(t, err)
	require.NotEmpty(t, authValue)

	joined := strings.Join(env, "\n")
	assert.NotContains(t, joined, token)
	assert.Contains(t, joined, "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader")
	assert.Contains(t, joined, "GIT_CONFIG_KEY_1=credential.helper")
	assert.Contains(t, joined, "GIT_CONFIG_VALUE_1=")

	encoded := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	assert.Contains(t, authValue, encoded)
}

func TestSanitizeWikiGitOutput(t *testing.T) {
	token := "ghp_test_secret_token"
	authValue := "AUTHORIZATION: basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	output := "fatal: " + token + " " + authValue
	sanitized := sanitizeWikiGitOutput(output, token, authValue)
	assert.NotContains(t, sanitized, token)
	assert.NotContains(t, sanitized, authValue)
	assert.Contains(t, sanitized, "[REDACTED]")
}

func TestWikiToolMetadata(t *testing.T) {
	getHead := WikiGetHead(translations.NullTranslationHelper)
	assert.Equal(t, "wiki_get_head", getHead.Tool.Name)
	require.NotNil(t, getHead.Tool.Annotations)
	assert.True(t, getHead.Tool.Annotations.ReadOnlyHint)

	publish := WikiPublishPages(translations.NullTranslationHelper)
	assert.Equal(t, "wiki_publish_pages", publish.Tool.Name)
	require.NotNil(t, publish.Tool.Annotations)
	assert.False(t, publish.Tool.Annotations.ReadOnlyHint)
	require.NotNil(t, publish.Tool.Annotations.DestructiveHint)
	assert.False(t, *publish.Tool.Annotations.DestructiveHint)
	assert.Equal(t, []string{"repo"}, publish.ScopeAccess.Scopes)
}
