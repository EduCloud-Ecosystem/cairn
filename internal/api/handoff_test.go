// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/EduCloud-Ecosystem/cairn/internal/handoff"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type handoffWriterFixture struct{ inspections int }

func (f *handoffWriterFixture) Inspect(_ context.Context, _ adapter.RepoRef, files []handoff.File) (handoff.Snapshot, error) {
	f.inspections++
	s := handoff.Snapshot{Branch: "refs/heads/main", BaseRevision: strings.Repeat("a", 40)}
	for _, file := range files {
		h := sha256.Sum256(file.Content)
		s.Changes = append(s.Changes, handoff.Change{Path: file.Path, Bytes: len(file.Content), SHA256: hex.EncodeToString(h[:]), Action: "replace"})
	}
	return s, nil
}
func (f *handoffWriterFixture) Prepare(context.Context, handoff.WriteRequest) (string, error) {
	return strings.Repeat("b", 40), nil
}
func (f *handoffWriterFixture) Publish(context.Context, handoff.WriteRequest, string) (handoff.Outcome, error) {
	return handoff.Committed, nil
}
func TestHandoffHTTPExplicitOriginOwnershipConfirmationAndReceipt(t *testing.T) {
	st := memory.New()
	seedStudentData(t, st)
	writer := &handoffWriterFixture{}
	service := &handoff.Service{Store: st, Writer: writer}
	srv := New(Options{Store: st, AuthEnabled: true, Handoff: service, HandoffOrigin: "https://cairn.example"})
	cookie := studentCookie(srv, adapter.HostGitHub, "alice")
	body, _ := json.Marshal(map[string]any{"idempotency_key": "request-001", "files": []handoff.File{{Path: "answer.py", Content: []byte("private-source")}}})
	request := func(method, path, origin string, body []byte, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Forwarded-Host", "attacker.example")
		r.Host = "attacker.example"
		if auth {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	route := "/me/work/s1/submission-previews"
	if w := request("POST", route, "https://cairn.example", body, false); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, origin := range []string{"", "null", "https://attacker.example"} {
		if w := request("POST", route, origin, body, true); w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := request("POST", "/me/work/s3/submission-previews", "https://cairn.example", body, true); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if writer.inspections != 0 {
		t.Fatal("unauthorized acquisition")
	}
	w := request("POST", route, "https://cairn.example", body, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var receipt handoff.Receipt
	if json.Unmarshal(w.Body.Bytes(), &receipt) != nil || receipt.Status != "preview" {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-source") || strings.Contains(w.Body.String(), "cHJpdmF0ZS1zb3VyY2U=") {
		t.Fatal("source echoed")
	}
	if w = request("POST", route+"/"+receipt.ID+"/confirm", "https://cairn.example", []byte(`{}`), true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := service.Process(context.Background(), receipt.ID); err != nil {
		t.Fatal(err)
	}
	if w = request("GET", "/me/work/s1/submission-receipts/"+receipt.ID, "", nil, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"committed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/me/work/s3/submission-receipts/"+receipt.ID, "", nil, true); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/me/work/s1/submission-receipts", "", nil, true); w.Code != 200 || !strings.Contains(w.Body.String(), receipt.ID) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestHandoffRejectsDeadlineChangeAndOversizedUpload(t *testing.T) {
	st := memory.New()
	seedStudentData(t, st)
	writer := &handoffWriterFixture{}
	service := &handoff.Service{Store: st, Writer: writer}
	srv := New(Options{Store: st, AuthEnabled: true, Handoff: service, HandoffOrigin: "http://127.0.0.1:8080"})
	cookie := studentCookie(srv, adapter.HostGitHub, "alice")
	call := func(path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://127.0.0.1:8080")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	route := "/me/work/s1/submission-previews"
	huge, _ := json.Marshal(map[string]any{"idempotency_key": "request-big", "files": []handoff.File{{Path: "answer.py", Content: make([]byte, handoff.MaxFileBytes+1)}}})
	if w := call(route, huge); w.Code != 400 {
		t.Fatal(w.Code)
	}
	body, _ := json.Marshal(map[string]any{"idempotency_key": "request-001", "files": []handoff.File{{Path: "answer.py", Content: []byte("42")}}})
	w := call(route, body)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var receipt handoff.Receipt
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	a, _ := st.GetAssignment(context.Background(), "a1")
	past := time.Now().Add(-time.Hour)
	a.Deadline = &past
	_ = st.UpdateAssignment(context.Background(), a)
	if w = call(route+"/"+receipt.ID+"/confirm", []byte(`{}`)); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestHandoffOriginConfiguration(t *testing.T) {
	for _, input := range []string{"https://user:secret@example.org/callback", "https://example.org/callback?token=x", "http://example.org/callback", "//example.org"} {
		if _, err := SubmissionOrigin(input); err == nil {
			t.Fatal("accepted", input)
		}
	}
	origin, err := SubmissionOrigin("https://cairn.example/auth/callback")
	if err != nil || origin != "https://cairn.example" {
		t.Fatal(origin, err)
	}
}
