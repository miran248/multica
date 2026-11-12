package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const (
	issueDeleteTestID1 = "11111111-1111-4111-8111-111111111111"
	issueDeleteTestID2 = "22222222-2222-4222-8222-222222222222"
)

func newIssueDeleteTestCmd(yes bool, output string) *cobra.Command {
	cmd := &cobra.Command{Use: "delete"}
	cmd.Flags().BoolP("yes", "y", yes, "")
	cmd.Flags().String("output", output, "")
	return cmd
}

func setIssueDeleteTestEnv(t *testing.T, serverURL string) {
	t.Helper()
	t.Setenv("MULTICA_SERVER_URL", serverURL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
}

type issueDeleteRequest struct {
	method string
	path   string
}

func TestRunIssueDeleteSingleByKey(t *testing.T) {
	var requests []issueDeleteRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, issueDeleteRequest{r.Method, r.URL.Path})
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues/MUL-1":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": issueDeleteTestID1, "identifier": "MUL-1"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/issues/"+issueDeleteTestID1:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	setIssueDeleteTestEnv(t, srv.URL)

	out, err := captureStdout(t, func() error {
		return runIssueDelete(newIssueDeleteTestCmd(true, "json"), []string{"MUL-1"})
	})
	if err != nil {
		t.Fatalf("run delete: %v", err)
	}

	want := []issueDeleteRequest{
		{http.MethodGet, "/api/issues/MUL-1"},
		{http.MethodDelete, "/api/issues/" + issueDeleteTestID1},
	}
	if !issueDeleteRequestsEqual(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}

	var result struct {
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != "MUL-1" {
		t.Fatalf("deleted = %v, want [MUL-1]", result.Deleted)
	}
}

// The batch route accepts UUIDs only and skips anything it cannot parse, so
// the CLI must resolve keys first. A key sent unchanged would produce
// {"deleted": 0} instead of failing.
func TestRunIssueDeleteBatchResolvesKeysToUUIDs(t *testing.T) {
	keys := map[string]string{"MUL-1": issueDeleteTestID1, "MUL-2": issueDeleteTestID2}
	var batchIDs []string
	var deleteCalls int
	var batchPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/issues/"):
			key := strings.TrimPrefix(r.URL.Path, "/api/issues/")
			id, ok := keys[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": id, "identifier": key})
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues/batch-delete":
			batchPath = r.URL.Path
			var body struct {
				IssueIDs []string `json:"issue_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode batch body: %v", err)
			}
			batchIDs = body.IssueIDs
			json.NewEncoder(w).Encode(map[string]any{"deleted": len(body.IssueIDs)})
		case r.Method == http.MethodDelete:
			deleteCalls++
			http.Error(w, "unexpected single delete", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	setIssueDeleteTestEnv(t, srv.URL)

	out, err := captureStdout(t, func() error {
		return runIssueDelete(newIssueDeleteTestCmd(true, "json"), []string{"MUL-1", "MUL-2"})
	})
	if err != nil {
		t.Fatalf("run delete: %v", err)
	}

	if deleteCalls != 0 {
		t.Fatalf("single delete calls = %d, want 0", deleteCalls)
	}
	if batchPath != "/api/issues/batch-delete" {
		t.Fatalf("batch path = %q", batchPath)
	}
	if len(batchIDs) != 2 || batchIDs[0] != issueDeleteTestID1 || batchIDs[1] != issueDeleteTestID2 {
		t.Fatalf("batch issue_ids = %v, want [%s %s]", batchIDs, issueDeleteTestID1, issueDeleteTestID2)
	}

	var result struct {
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if len(result.Deleted) != 2 || result.Deleted[0] != "MUL-1" || result.Deleted[1] != "MUL-2" {
		t.Fatalf("deleted = %v, want [MUL-1 MUL-2]", result.Deleted)
	}
}

func TestRunIssueDeleteDuplicateKeysDeleteOnce(t *testing.T) {
	deleteCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": issueDeleteTestID1, "identifier": "MUL-1"})
		case http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	setIssueDeleteTestEnv(t, srv.URL)

	_, err := captureStdout(t, func() error {
		return runIssueDelete(newIssueDeleteTestCmd(true, "json"), []string{"MUL-1", "MUL-1"})
	})
	if err != nil {
		t.Fatalf("run delete: %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
}

func TestRunIssueDeletePromptAborts(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	setIssueDeleteTestEnv(t, srv.URL)

	stderr := captureStderr(t)
	out, err := captureStdout(t, func() error {
		var runErr error
		pipeStdin(t, "n\n", func() {
			runErr = runIssueDelete(newIssueDeleteTestCmd(false, "table"), []string{issueDeleteTestID1})
		})
		return runErr
	})
	if err != nil {
		t.Fatalf("run delete: %v", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0 after declining the prompt", requests)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	msg := stderr.read()
	if !strings.Contains(msg, "Delete issue "+issueDeleteTestID1) {
		t.Fatalf("prompt = %q, want it to name the issue", msg)
	}
	if !strings.Contains(msg, "cannot be undone") {
		t.Fatalf("prompt = %q, want it to state permanence", msg)
	}
	if !strings.Contains(msg, "Aborted.") {
		t.Fatalf("stderr = %q, want Aborted.", msg)
	}
}

func TestRunIssueDeletePromptAcceptsYes(t *testing.T) {
	deleteCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		deleteCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	setIssueDeleteTestEnv(t, srv.URL)

	_, err := captureStdout(t, func() error {
		var runErr error
		pipeStdin(t, "y\n", func() {
			runErr = runIssueDelete(newIssueDeleteTestCmd(false, "table"), []string{issueDeleteTestID1})
		})
		return runErr
	})
	if err != nil {
		t.Fatalf("run delete: %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
}

// Resolution happens before the prompt and before the batch request, so one
// unknown key aborts the whole batch instead of deleting the resolvable ones.
func TestRunIssueDeleteBatchAbortsOnUnknownKey(t *testing.T) {
	var wrote bool
	var deletes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues/MUL-1":
			json.NewEncoder(w).Encode(map[string]any{"id": issueDeleteTestID1, "identifier": "MUL-1"})
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "issue not found"})
		default:
			wrote = true
			deletes++
			t.Errorf("unexpected write %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	setIssueDeleteTestEnv(t, srv.URL)

	err := runIssueDelete(newIssueDeleteTestCmd(true, "json"), []string{"MUL-1", "MUL-404"})
	if err == nil || !strings.Contains(err.Error(), `resolve issue "MUL-404"`) {
		t.Fatalf("error = %v, want a resolve failure for MUL-404", err)
	}
	if wrote || deletes != 0 {
		t.Fatalf("wrote = %v, deletes = %d, want no writes", wrote, deletes)
	}
}

func TestRunIssueDeleteBatchCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/issues/batch-delete" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"deleted": 1})
	}))
	defer srv.Close()
	setIssueDeleteTestEnv(t, srv.URL)

	err := runIssueDelete(newIssueDeleteTestCmd(true, "json"), []string{issueDeleteTestID1, issueDeleteTestID2})
	if err == nil || !strings.Contains(err.Error(), "deleted 1 of 2 issues") {
		t.Fatalf("error = %v, want a partial-batch error", err)
	}
}

func TestRunIssueDeletePermissionErrorIsSurfaced(t *testing.T) {
	const serverMessage = "issue deletion is restricted to its creator, the creating agent's owner, and workspace owners or admins"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": serverMessage})
	}))
	defer srv.Close()
	setIssueDeleteTestEnv(t, srv.URL)

	err := runIssueDelete(newIssueDeleteTestCmd(true, "table"), []string{issueDeleteTestID1})
	if err == nil || !strings.Contains(err.Error(), serverMessage) {
		t.Fatalf("error = %v, want the server's %q", err, serverMessage)
	}
}

func issueDeleteRequestsEqual(got, want []issueDeleteRequest) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
