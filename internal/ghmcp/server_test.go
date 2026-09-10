package ghmcp

import (
	"context"
	"testing"

	ghcontext "github.com/github/github-mcp-server/pkg/context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateTokenContextMiddlewareInjectsProviderToken(t *testing.T) {
	providerCalls := 0
	provider := func() string {
		providerCalls++
		return "request-principal-token"
	}
	seen := ""
	next := func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		info, ok := ghcontext.GetTokenInfo(ctx)
		require.True(t, ok)
		require.NotNil(t, info)
		seen = info.Token
		return &mcp.CallToolResult{}, nil
	}

	handler := createTokenContextMiddleware(provider)(next)
	_, err := handler(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
	assert.Equal(t, 1, providerCalls)
	assert.Equal(t, "request-principal-token", seen)
}

func TestCreateTokenContextMiddlewareDoesNotInventEmptyCredential(t *testing.T) {
	next := func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, ok := ghcontext.GetTokenInfo(ctx)
		assert.False(t, ok)
		return &mcp.CallToolResult{}, nil
	}

	handler := createTokenContextMiddleware(func() string { return "" })(next)
	_, err := handler(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
}
