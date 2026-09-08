from pathlib import Path

wiki = Path("pkg/github/wiki.go")
s = wiki.read_text()

old = "DestructiveHint: jsonschema.Ptr(false),"
assert s.count(old) == 1, s.count(old)
s = s.replace(old, "DestructiveHint: jsonschema.Ptr(true),")

old = "content, err := os.ReadFile(filepath.Join(checkout, path))"
assert s.count(old) == 1, s.count(old)
s = s.replace(old, "content, err := readWikiPageNoFollow(checkout, path)")

old = """\t\t\tfor _, page := range pages {
\t\t\t\tif err := os.WriteFile(filepath.Join(checkout, page.Path), []byte(page.Content), 0o600); err != nil {
\t\t\t\t\treturn utils.NewToolResultError(fmt.Sprintf(\"failed to write Wiki page %q: %v\", page.Path, err)), nil, nil
\t\t\t\t}
\t\t\t\tpagePaths = append(pagePaths, page.Path)
\t\t\t}
"""
new = """\t\t\tfor _, page := range pages {
\t\t\t\tif err := writeWikiPageNoFollow(checkout, page.Path, []byte(page.Content)); err != nil {
\t\t\t\t\treturn utils.NewToolResultError(fmt.Sprintf(\"failed to write Wiki page %q: %v\", page.Path, err)), nil, nil
\t\t\t\t}
\t\t\t\tpagePaths = append(pagePaths, page.Path)
\t\t\t}
"""
assert s.count(old) == 1, s.count(old)
s = s.replace(old, new)

old = """func wikiGitToken(ctx context.Context) string {
\tif tokenInfo, ok := ghcontext.GetTokenInfo(ctx); ok && tokenInfo != nil && tokenInfo.Token != \"\" {
\t\treturn tokenInfo.Token
\t}
\tfor _, name := range []string{\"GITHUB_PERSONAL_ACCESS_TOKEN\", \"GH_TOKEN\"} {
\t\tif token := os.Getenv(name); token != \"\" {
\t\t\treturn token
\t\t}
\t}
\treturn \"\"
}
"""
new = """func wikiGitToken(ctx context.Context) string {
\tif tokenInfo, ok := ghcontext.GetTokenInfo(ctx); ok && tokenInfo != nil && tokenInfo.Token != \"\" {
\t\treturn tokenInfo.Token
\t}
\treturn \"\"
}
"""
assert s.count(old) == 1, s.count(old)
s = s.replace(old, new)

marker = "func validateWikiHead(head string) error {\n"
assert s.count(marker) == 1
helpers = """func readWikiPageNoFollow(checkout, pagePath string) ([]byte, error) {
\tpath := filepath.Join(checkout, pagePath)
\tinfo, err := os.Lstat(path)
\tif err != nil {
\t\treturn nil, err
\t}
\tif info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
\t\treturn nil, fmt.Errorf(\"Wiki page %q is not a regular non-symlink file\", pagePath)
\t}
\treturn os.ReadFile(path)
}

func writeWikiPageNoFollow(checkout, pagePath string, content []byte) (err error) {
\tpath := filepath.Join(checkout, pagePath)
\tinfo, statErr := os.Lstat(path)
\texists := statErr == nil
\tif statErr != nil && !os.IsNotExist(statErr) {
\t\treturn statErr
\t}
\tif exists && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
\t\treturn fmt.Errorf(\"Wiki page %q is not a regular non-symlink file\", pagePath)
\t}

\ttmp, err := os.CreateTemp(checkout, \".wiki-page-*\")
\tif err != nil {
\t\treturn err
\t}
\ttmpName := tmp.Name()
\tdefer func() {
\t\t_ = tmp.Close()
\t\t_ = os.Remove(tmpName)
\t}()
\tif err := tmp.Chmod(0o600); err != nil {
\t\treturn err
\t}
\tif _, err := tmp.Write(content); err != nil {
\t\treturn err
\t}
\tif err := tmp.Sync(); err != nil {
\t\treturn err
\t}
\tif err := tmp.Close(); err != nil {
\t\treturn err
\t}
\tif exists {
\t\tif err := os.Remove(path); err != nil {
\t\t\treturn err
\t\t}
\t}
\tif err := os.Rename(tmpName, path); err != nil {
\t\treturn err
\t}
\treturn nil
}

"""
s = s.replace(marker, helpers + marker)
wiki.write_text(s)

tests = Path("pkg/github/wiki_test.go")
s = tests.read_text()
old = '\t"os"\n\t"strings"\n'
assert s.count(old) == 1, s.count(old)
s = s.replace(old, '\t"os"\n\t"path/filepath"\n\t"strings"\n')

old = """func TestWikiGitTokenPrefersRequestContext(t *testing.T) {
\tt.Setenv(\"GITHUB_PERSONAL_ACCESS_TOKEN\", \"process-personal-token\")
\tt.Setenv(\"GH_TOKEN\", \"process-gh-token\")
\tctx := ghcontext.WithTokenInfo(context.Background(), &ghcontext.TokenInfo{Token: \"request-token\"})
\tassert.Equal(t, \"request-token\", wikiGitToken(ctx))
}
"""
new = old + """
func TestWikiGitTokenDoesNotFallBackToAmbientCredential(t *testing.T) {
\tt.Setenv(\"GITHUB_PERSONAL_ACCESS_TOKEN\", \"ambient-owner-token\")
\tt.Setenv(\"GH_TOKEN\", \"ambient-gh-token\")
\tassert.Empty(t, wikiGitToken(context.Background()))
}
"""
assert s.count(old) == 1, s.count(old)
s = s.replace(old, new)

marker = "func TestWikiToolMetadata(t *testing.T) {\n"
assert s.count(marker) == 1
hostile = """func TestWikiPageIORejectsSymlinksWithoutTouchingTarget(t *testing.T) {
\tfor _, tc := range []struct {
\t\tname     string
\t\trelative bool
\t}{
\t\t{name: \"absolute\", relative: false},
\t\t{name: \"relative\", relative: true},
\t} {
\t\tt.Run(tc.name, func(t *testing.T) {
\t\t\troot := t.TempDir()
\t\t\tcheckout := filepath.Join(root, \"checkout\")
\t\t\trequire.NoError(t, os.Mkdir(checkout, 0o700))
\t\t\toutside := filepath.Join(root, \"outside.txt\")
\t\t\trequire.NoError(t, os.WriteFile(outside, []byte(\"outside-secret\"), 0o600))

\t\t\ttarget := outside
\t\t\tif tc.relative {
\t\t\t\ttarget = filepath.Join(\"..\", \"outside.txt\")
\t\t\t}
\t\t\tpage := filepath.Join(checkout, \"Home.md\")
\t\t\tif err := os.Symlink(target, page); err != nil {
\t\t\t\tt.Skipf(\"symlink creation unavailable on this runner: %v\", err)
\t\t\t}

\t\t\tcontent, err := readWikiPageNoFollow(checkout, \"Home.md\")
\t\t\trequire.Error(t, err)
\t\t\tassert.Nil(t, content)
\t\t\tassert.Contains(t, err.Error(), \"non-symlink\")

\t\t\terr = writeWikiPageNoFollow(checkout, \"Home.md\", []byte(\"replacement\"))
\t\t\trequire.Error(t, err)
\t\t\tassert.Contains(t, err.Error(), \"non-symlink\")

\t\t\toutsideBytes, readErr := os.ReadFile(outside)
\t\t\trequire.NoError(t, readErr)
\t\t\tassert.Equal(t, \"outside-secret\", string(outsideBytes))
\t\t})
\t}
}

func TestWikiPageWriteCreatesAndReplacesRegularFile(t *testing.T) {
\tcheckout := t.TempDir()
\trequire.NoError(t, writeWikiPageNoFollow(checkout, \"Home.md\", []byte(\"one\")))
\tgot, err := readWikiPageNoFollow(checkout, \"Home.md\")
\trequire.NoError(t, err)
\tassert.Equal(t, \"one\", string(got))

\trequire.NoError(t, writeWikiPageNoFollow(checkout, \"Home.md\", []byte(\"two\")))
\tgot, err = readWikiPageNoFollow(checkout, \"Home.md\")
\trequire.NoError(t, err)
\tassert.Equal(t, \"two\", string(got))
}

"""
s = s.replace(marker, hostile + marker)

old = '\tassert.False(t, *publish.Tool.Annotations.DestructiveHint)\n'
assert s.count(old) == 1, s.count(old)
s = s.replace(old, '\tassert.True(t, *publish.Tool.Annotations.DestructiveHint)\n')
tests.write_text(s)
