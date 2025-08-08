package server

import (
    "context"
    "fmt"
    "net"
    "testing"
    "time"

    "github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/database"
    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/stretchr/testify/require"
)

// pickFreePort tries to get a free TCP port on 127.0.0.1
func pickFreePort() (int, error) {
    l, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        return 0, err
    }
    defer l.Close()
    return l.Addr().(*net.TCPAddr).Port, nil
}

func TestSSEServer_ListTools(t *testing.T) {
    cfg := database.NewConfig()
    cfg.URL = "file:test-e2e?mode=memory&cache=shared"
    cfg.EmbeddingDims = 4
    dbm, err := database.NewDBManager(cfg)
    require.NoError(t, err)
    defer dbm.Close()

    srv := NewMCPServer(dbm)

    port, err := pickFreePort()
    require.NoError(t, err)
    addr := fmt.Sprintf("127.0.0.1:%d", port)
    endpoint := "/sse"

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // start SSE server
    go func() { _ = srv.RunSSE(ctx, addr, endpoint) }()

    // wait briefly for server to bind
    time.Sleep(150 * time.Millisecond)

    // connect with MCP SSE client
    client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "test"}, nil)
    transport := mcp.NewSSEClientTransport("http://"+addr+endpoint, nil)

    // retry connect a few times to avoid flakes
    var session *mcp.ClientSession
    for i := 0; i < 5; i++ {
        session, err = client.Connect(ctx, transport)
        if err == nil {
            break
        }
        time.Sleep(100 * time.Millisecond)
    }
    require.NoError(t, err)
    defer session.Close()

    tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
    require.NoError(t, err)
    require.NotEmpty(t, tools.Tools)
}

