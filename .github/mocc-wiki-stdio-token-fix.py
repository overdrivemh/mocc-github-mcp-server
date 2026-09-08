from pathlib import Path

server = Path("internal/ghmcp/server.go")
s = server.read_text()

old = '"github.com/github/github-mcp-server/internal/requeststate"\n\t"github.com/github/github-mcp-server/pkg/errors"'
new = '"github.com/github/github-mcp-server/internal/requeststate"\n\tghcontext "github.com/github/github-mcp-server/pkg/context"\n\t"github.com/github/github-mcp-server/pkg/errors"'
assert s.count(old) == 1, s.count(old)
s = s.replace(old, new)

old = '''\ttokenProvider := cfg.TokenProvider
\tvar toolHandlerMiddleware []inventory.ToolHandlerMiddleware
\tif cfg.OAuthManager != nil {
\t\ttokenProvider = cfg.OAuthManager.AccessToken
\t\ttoolHandlerMiddleware = append(toolHandlerMiddleware, createOAuthToolMiddleware(cfg.OAuthManager, logger))
\t}
'''
new = '''\ttokenProvider := cfg.TokenProvider
\tvar toolHandlerMiddleware []inventory.ToolHandlerMiddleware
\tif cfg.OAuthManager != nil {
\t\ttokenProvider = cfg.OAuthManager.AccessToken
\t\ttoolHandlerMiddleware = append(toolHandlerMiddleware, createOAuthToolMiddleware(cfg.OAuthManager, logger))
\t}

\t// Project the same stdio authentication principal used by the GitHub API
\t// clients into tool request context. Git-backed tools can then use that
\t// request-scoped credential without falling back to ambient process secrets.
\tcontextTokenProvider := tokenProvider
\tif cfg.Token != "" {
\t\tcontextTokenProvider = func() string { return cfg.Token }
\t}
\tif contextTokenProvider != nil {
\t\ttoolHandlerMiddleware = append(toolHandlerMiddleware, createTokenContextMiddleware(contextTokenProvider))
\t}
'''
assert s.count(old) == 1, s.count(old)
s = s.replace(old, new)

marker = '''func createFeatureChecker(enabledFeatures []string, insidersMode bool) inventory.FeatureFlagChecker {
'''
assert s.count(marker) == 1
helper = '''func createTokenContextMiddleware(tokenProvider func() string) inventory.ToolHandlerMiddleware {
\treturn func(next mcp.ToolHandler) mcp.ToolHandler {
\t\treturn func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
\t\t\ttoken := tokenProvider()
\t\t\tif token != "" {
\t\t\t\tctx = ghcontext.WithTokenInfo(ctx, &ghcontext.TokenInfo{Token: token})
\t\t\t}
\t\t\treturn next(ctx, req)
\t\t}
\t}
}

'''
s = s.replace(marker, helper + marker)
server.write_text(s)

test = Path("internal/ghmcp/server_test.go")
assert test.read_text() == "package ghmcp\n"
test.write_text('''package ghmcp

import (
\t"context"
\t"testing"

\tghcontext "github.com/github/github-mcp-server/pkg/context"
\t"github.com/modelcontextprotocol/go-sdk/mcp"
\t"github.com/stretchr/testify/assert"
\t"github.com/stretchr/testify/require"
)

func TestCreateTokenContextMiddlewareInjectsProviderToken(t *testing.T) {
\tproviderCalls := 0
\tprovider := func() string {
\t\tproviderCalls++
\t\treturn "request-principal-token"
\t}
\tseen := ""
\tnext := func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
\t\tinfo, ok := ghcontext.GetTokenInfo(ctx)
\t\trequire.True(t, ok)
\t\trequire.NotNil(t, info)
\t\tseen = info.Token
\t\treturn &mcp.CallToolResult{}, nil
\t}

\thandler := createTokenContextMiddleware(provider)(next)
\t_, err := handler(context.Background(), &mcp.CallToolRequest{})
\trequire.NoError(t, err)
\tassert.Equal(t, 1, providerCalls)
\tassert.Equal(t, "request-principal-token", seen)
}

func TestCreateTokenContextMiddlewareDoesNotInventEmptyCredential(t *testing.T) {
\tnext := func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
\t\t_, ok := ghcontext.GetTokenInfo(ctx)
\t\tassert.False(t, ok)
\t\treturn &mcp.CallToolResult{}, nil
\t}

\thandler := createTokenContextMiddleware(func() string { return "" })(next)
\t_, err := handler(context.Background(), &mcp.CallToolRequest{})
\trequire.NoError(t, err)
}
''')
