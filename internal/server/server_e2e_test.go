package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/database"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
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
	for range 5 {
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

func TestSSEServer_ConcurrentClients(t *testing.T) {
	cfg := database.NewConfig()
	cfg.URL = "file:test-e2e-concurrent?mode=memory&cache=shared"
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
	time.Sleep(150 * time.Millisecond)

	// launch multiple concurrent clients
	const clients = 16
	errCh := make(chan error, clients)

	for range clients {
		go func() {
			c := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "test"}, nil)
			transport := mcp.NewSSEClientTransport("http://"+addr+endpoint, nil)
			session, err := c.Connect(ctx, transport)
			if err != nil {
				errCh <- err
				return
			}
			defer session.Close()
			_, err = session.ListTools(ctx, &mcp.ListToolsParams{})
			errCh <- err
		}()
	}

	for range clients {
		require.NoError(t, <-errCh)
	}
}

func TestSSEServer_ToolCallsE2E(t *testing.T) {
	cfg := database.NewConfig()
	cfg.URL = "file:test-e2e-tools?mode=memory&cache=shared"
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
	go func() { _ = srv.RunSSE(ctx, addr, endpoint) }()
	time.Sleep(150 * time.Millisecond)

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "test"}, nil)
	transport := mcp.NewSSEClientTransport("http://"+addr+endpoint, nil)
	session, err := client.Connect(ctx, transport)
	require.NoError(t, err)
	defer session.Close()

	// 1) create_entities
	createArgs := apptype.CreateEntitiesArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: "default"},
		Entities: []apptype.Entity{
			{Name: "n1", EntityType: "t", Observations: []string{"o1"}},
			{Name: "n2", EntityType: "t", Observations: []string{"o2"}},
		},
	}
	createRaw, _ := json.Marshal(createArgs)
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "create_entities", Arguments: json.RawMessage(createRaw)})
	require.NoError(t, err)

	// 2) search_nodes (text)
	searchArgs := apptype.SearchNodesArgs{ProjectArgs: apptype.ProjectArgs{ProjectName: "default"}, Query: "n", Limit: 10}
	searchRaw, _ := json.Marshal(searchArgs)
	sres, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_nodes", Arguments: json.RawMessage(searchRaw)})
	require.NoError(t, err)
	// decode structured content
	gr := decodeStructuredGraphResult(sres)
	assert.GreaterOrEqual(t, len(gr.Entities), 2)

	// 3) read_graph
	readArgs := apptype.ReadGraphArgs{ProjectArgs: apptype.ProjectArgs{ProjectName: "default"}, Limit: 10}
	readRaw, _ := json.Marshal(readArgs)
	rres, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_graph", Arguments: json.RawMessage(readRaw)})
	require.NoError(t, err)
	gr2 := decodeStructuredGraphResult(rres)
	assert.GreaterOrEqual(t, len(gr2.Entities), 2)
}

func TestSSEServer_GraphToolsE2E(t *testing.T) {
	cfg := database.NewConfig()
	cfg.URL = "file:test-e2e-graph?mode=memory&cache=shared"
	cfg.EmbeddingDims = 4
	dbm, err := database.NewDBManager(cfg)
	require.NoError(t, err)
	defer dbm.Close()

	// seed graph directly via DB
	ctx := context.Background()
	require.NoError(t, dbm.CreateEntities(ctx, "default", []apptype.Entity{
		{Name: "a", EntityType: "t", Observations: []string{"oa"}},
		{Name: "b", EntityType: "t", Observations: []string{"ob"}},
		{Name: "c", EntityType: "t", Observations: []string{"oc"}},
		{Name: "d", EntityType: "t", Observations: []string{"od"}},
	}))
	require.NoError(t, dbm.CreateRelations(ctx, "default", []apptype.Relation{
		{From: "a", To: "b", RelationType: "r"},
		{From: "b", To: "c", RelationType: "r"},
		{From: "a", To: "d", RelationType: "r"},
	}))

	srv := NewMCPServer(dbm)
	port, err := pickFreePort()
	require.NoError(t, err)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	endpoint := "/sse"

	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.RunSSE(sctx, addr, endpoint) }()
	time.Sleep(150 * time.Millisecond)

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "test"}, nil)
	transport := mcp.NewSSEClientTransport("http://"+addr+endpoint, nil)
	session, err := client.Connect(sctx, transport)
	require.NoError(t, err)
	defer session.Close()

	// neighbors(out) from a
	nArgs := map[string]any{
		"projectArgs": map[string]any{"projectName": "default"},
		"names":       []string{"a"},
		"direction":   "out",
	}
	nRaw, _ := json.Marshal(nArgs)
	nres, err := session.CallTool(sctx, &mcp.CallToolParams{Name: "neighbors", Arguments: json.RawMessage(nRaw)})
	require.NoError(t, err)
	ngr := decodeStructuredGraphResult(nres)
	// Expect at least a and its two neighbors
	require.GreaterOrEqual(t, len(ngr.Entities), 3)

	// walk depth 2 from a
	wArgs := map[string]any{
		"projectArgs": map[string]any{"projectName": "default"},
		"names":       []string{"a"},
		"maxDepth":    2,
		"direction":   "out",
	}
	wRaw, _ := json.Marshal(wArgs)
	wres, err := session.CallTool(sctx, &mcp.CallToolParams{Name: "walk", Arguments: json.RawMessage(wRaw)})
	require.NoError(t, err)
	wgr := decodeStructuredGraphResult(wres)
	require.GreaterOrEqual(t, len(wgr.Entities), 4)

	// shortest_path a->c
	spArgs := map[string]any{
		"projectArgs": map[string]any{"projectName": "default"},
		"from":        "a",
		"to":          "c",
		"direction":   "out",
	}
	spRaw, _ := json.Marshal(spArgs)
	spres, err := session.CallTool(sctx, &mcp.CallToolParams{Name: "shortest_path", Arguments: json.RawMessage(spRaw)})
	require.NoError(t, err)
	spgr := decodeStructuredGraphResult(spres)
	require.GreaterOrEqual(t, len(spgr.Entities), 3)
}

// decodeStructuredGraphResult attempts to unmarshal the structured content of a CallToolResult
// into GraphResult, handling the various concrete types used by the SDK.
func decodeStructuredGraphResult(res *mcp.CallToolResult) apptype.GraphResult {
	var out apptype.GraphResult
	if res == nil || res.StructuredContent == nil {
		return out
	}
	switch v := res.StructuredContent.(type) {
	case json.RawMessage:
		_ = json.Unmarshal(v, &out)
	case *json.RawMessage:
		_ = json.Unmarshal(*v, &out)
	case []byte:
		_ = json.Unmarshal(v, &out)
	case map[string]any:
		if b, err := json.Marshal(v); err == nil {
			_ = json.Unmarshal(b, &out)
		}
	default:
		if b, err := json.Marshal(v); err == nil {
			_ = json.Unmarshal(b, &out)
		}
	}
	return out
}
