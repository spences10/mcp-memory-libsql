package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type StepResult struct {
	Name      string `json:"name"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

type Report struct {
	SSEURL     string       `json:"sse_url"`
	StartedAt  time.Time    `json:"started_at"`
	DurationMs int64        `json:"duration_ms"`
	Steps      []StepResult `json:"steps"`
	Passed     bool         `json:"passed"`
}

func main() {
	sseURL := flag.String("sse-url", "http://localhost:8080/sse", "SSE endpoint URL")
	project := flag.String("project", "default", "Project name to use")
	timeout := flag.Duration("timeout", 30*time.Second, "Overall timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "integration-tester", Version: "dev"}, nil)
	transport := mcp.NewSSEClientTransport(*sseURL, nil)

	start := time.Now()
	report := Report{SSEURL: *sseURL, StartedAt: start}
	steps := make([]StepResult, 0, 16)

	// Connect
	tConn := time.Now()
	connRes := StepResult{Name: "connect"}
	session, err := client.Connect(ctx, transport)
	if err != nil {
		connRes.Success = false
		connRes.Error = err.Error()
		connRes.ElapsedMs = elapsedMsSince(tConn)
		steps = append(steps, connRes)
		report.Steps = steps
		report.DurationMs = elapsedMsSince(start)
		report.Passed = false
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		os.Exit(1)
	}
	defer session.Close()
	connRes.Success = true
	connRes.ElapsedMs = elapsedMsSince(tConn)
	steps = append(steps, connRes)

	// Individual steps
	steps = append(steps, runListTools(ctx, session))
	steps = append(steps, runCreateEntities(ctx, session, *project))
	steps = append(steps, runSearchNodes(ctx, session, *project, "n"))
	steps = append(steps, runReadGraph(ctx, session, *project))
	steps = append(steps, runSeedGraph(ctx, session, *project))
	steps = append(steps, runNeighbors(ctx, session, *project))
	steps = append(steps, runWalk(ctx, session, *project))
	steps = append(steps, runShortestPath(ctx, session, *project))
	// DELETE tests on fresh instance
	steps = append(steps, runDeleteRelation(ctx, session, *project, "b", "c", "r"))
	steps = append(steps, runDeleteRelations(ctx, session, *project, []apptype.RelationTuple{{From: "a", To: "d", RelationType: "r"}}))
	steps = append(steps, runDeleteObservationsByContents(ctx, session, *project, "a", []string{"oa"}))
	steps = append(steps, runDeleteEntity(ctx, session, *project, "n1"))
	steps = append(steps, runDeleteEntities(ctx, session, *project, []string{"a", "b"}))

	// finalize report
	report.Steps = steps
	report.DurationMs = elapsedMsSince(start)
	report.Passed = true
	for _, s := range steps {
		if !s.Success {
			report.Passed = false
			break
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)

	if !report.Passed {
		os.Exit(1)
	}
}

func runListTools(ctx context.Context, session *mcp.ClientSession) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "list_tools"}
	if _, err := session.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runCreateEntities(ctx context.Context, session *mcp.ClientSession, project string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "create_entities"}
	args := apptype.CreateEntitiesArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: project},
		Entities: []apptype.Entity{
			{Name: "n1", EntityType: "t", Observations: []string{"o1"}},
			{Name: "n2", EntityType: "t", Observations: []string{"o2"}},
		},
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_entities", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runSearchNodes(ctx context.Context, session *mcp.ClientSession, project, q string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "search_nodes"}
	args := apptype.SearchNodesArgs{ProjectArgs: apptype.ProjectArgs{ProjectName: project}, Query: q, Limit: 10}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_nodes", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runReadGraph(ctx context.Context, session *mcp.ClientSession, project string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "read_graph"}
	args := apptype.ReadGraphArgs{ProjectArgs: apptype.ProjectArgs{ProjectName: project}, Limit: 10}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_graph", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runSeedGraph(ctx context.Context, session *mcp.ClientSession, project string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "seed_graph"}
	// a,b,c,d
	ca := apptype.CreateEntitiesArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: project},
		Entities: []apptype.Entity{
			{Name: "a", EntityType: "t", Observations: []string{"oa"}},
			{Name: "b", EntityType: "t", Observations: []string{"ob"}},
			{Name: "c", EntityType: "t", Observations: []string{"oc"}},
			{Name: "d", EntityType: "t", Observations: []string{"od"}},
		},
	}
	raw, _ := json.Marshal(ca)
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_entities", Arguments: json.RawMessage(raw)}); err != nil {
		res.Success = false
		res.Error = fmt.Sprintf("create_entities seed: %v", err)
		res.ElapsedMs = elapsedMsSince(t0)
		return res
	}
	// relations: a->b, b->c, a->d
	cr := apptype.CreateRelationsArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: project},
		Relations:   []apptype.Relation{{From: "a", To: "b", RelationType: "r"}, {From: "b", To: "c", RelationType: "r"}, {From: "a", To: "d", RelationType: "r"}},
	}
	rraw, _ := json.Marshal(cr)
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_relations", Arguments: json.RawMessage(rraw)}); err != nil {
		res.Success = false
		res.Error = fmt.Sprintf("create_relations seed: %v", err)
		res.ElapsedMs = elapsedMsSince(t0)
		return res
	}
	res.Success = true
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runNeighbors(ctx context.Context, session *mcp.ClientSession, project string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "neighbors"}
	args := map[string]any{
		"projectArgs": map[string]any{"projectName": project},
		"names":       []string{"a"},
		"direction":   "out",
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "neighbors", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runWalk(ctx context.Context, session *mcp.ClientSession, project string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "walk"}
	args := map[string]any{
		"projectArgs": map[string]any{"projectName": project},
		"names":       []string{"a"},
		"maxDepth":    2,
		"direction":   "out",
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "walk", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runShortestPath(ctx context.Context, session *mcp.ClientSession, project string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "shortest_path"}
	args := map[string]any{
		"projectArgs": map[string]any{"projectName": project},
		"from":        "a",
		"to":          "c",
		"direction":   "out",
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "shortest_path", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runDeleteRelation(ctx context.Context, session *mcp.ClientSession, project, from, to, relType string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "delete_relation"}
	args := apptype.DeleteRelationArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: project},
		Source:      from,
		Target:      to,
		Type:        relType,
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "delete_relation", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runDeleteRelations(ctx context.Context, session *mcp.ClientSession, project string, tuples []apptype.RelationTuple) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "delete_relations"}
	args := apptype.DeleteRelationsArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: project},
		Relations:   tuples,
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "delete_relations", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runDeleteObservationsByContents(ctx context.Context, session *mcp.ClientSession, project, entity string, contents []string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "delete_observations"}
	args := apptype.DeleteObservationsArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: project},
		EntityName:  entity,
		Contents:    contents,
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "delete_observations", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runDeleteEntity(ctx context.Context, session *mcp.ClientSession, project, name string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "delete_entity"}
	args := apptype.DeleteEntityArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: project},
		Name:        name,
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "delete_entity", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

func runDeleteEntities(ctx context.Context, session *mcp.ClientSession, project string, names []string) StepResult {
	t0 := time.Now()
	res := StepResult{Name: "delete_entities"}
	args := apptype.DeleteEntitiesArgs{
		ProjectArgs: apptype.ProjectArgs{ProjectName: project},
		Names:       names,
	}
	raw, _ := json.Marshal(args)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "delete_entities", Arguments: json.RawMessage(raw)})
	if err != nil {
		res.Success = false
		res.Error = err.Error()
	} else {
		res.Success = true
	}
	res.ElapsedMs = elapsedMsSince(t0)
	return res
}

// elapsedMsSince returns max(1ms, elapsed) to avoid zero durations on fast steps
func elapsedMsSince(t0 time.Time) int64 {
	d := time.Since(t0) / time.Millisecond
	if d <= 0 {
		return 1
	}
	return int64(d)
}
