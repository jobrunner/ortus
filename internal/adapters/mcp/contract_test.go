package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// updateGolden regenerates testdata/mcp_tools.golden.json:
//
//	go test ./internal/adapters/mcp/ -run TestMCPToolContract -update-golden
var updateGolden = flag.Bool("update-golden", false, "rewrite the MCP tool golden file")

// TestMCPToolContract freezes the agent-facing MCP surface — tool names and
// their input JSON Schemas — as a golden snapshot. Tool names and argument
// schemas are part of the AI-agent contract (a rename like list_packages →
// list_sources, or a changed/removed argument, breaks every configured agent),
// so any change must be a deliberate, reviewed golden update. Descriptions are
// intentionally excluded — they are prose and may change freely.
func TestMCPToolContract(t *testing.T) {
	ts := startTestServer(t, "")

	client := mcp.NewClient(&mcp.Implementation{Name: "contract-test"}, nil)
	session, err := client.Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	type toolContract struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	got := make([]toolContract, 0, len(list.Tools))
	for _, tl := range list.Tools {
		schema, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("marshal schema for %q: %v", tl.Name, err)
		}
		got = append(got, toolContract{Name: tl.Name, InputSchema: schema})
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })

	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal contract: %v", err)
	}

	golden := filepath.Join("testdata", "mcp_tools.golden.json")
	if *updateGolden {
		if err := os.WriteFile(golden, append(gotJSON, '\n'), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", golden)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update-golden to create): %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(gotJSON)) {
		t.Errorf("MCP tool contract drift.\nIf intended, regenerate:\n"+
			"  go test ./internal/adapters/mcp/ -run TestMCPToolContract -update-golden\n\n--- got ---\n%s", gotJSON)
	}
}
