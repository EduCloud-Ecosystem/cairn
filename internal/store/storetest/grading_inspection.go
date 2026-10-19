// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func testGradingInspection(t *testing.T, s store.Store) {
	ctx := context.Background()
	for _, id := range []string{"Z", "_", "a", "a-1", "a_1"} {
		if _, err := s.CreateJob(ctx, &store.ProvisioningJob{ID: id, Type: "grade", TargetRef: "private legacy target", Status: store.JobInProgress, IdempotencyKey: id, ScheduledAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateJob(ctx, &store.ProvisioningJob{ID: "hidden", Type: "create_repo", TargetRef: "private", Status: store.JobPending, IdempotencyKey: "hidden", ScheduledAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	after := ""
	for page := 0; page < 4; page++ {
		rows, err := s.ReadGradingInspection(ctx, store.GradingInspectionQuery{After: after, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			ids = append(ids, r.Job.ID)
			if r.HasDispatch || r.RunPresent || r.GradePresent {
				t.Fatal("invented publication")
			}
			b, err := json.Marshal(r)
			if err != nil || string(b) != "{}" {
				t.Fatal("private row serialized", string(b), err)
			}
		}
		if len(rows) < 2 {
			break
		}
		after = rows[len(rows)-1].Job.ID
	}
	if !reflect.DeepEqual(ids, []string{"Z", "_", "a", "a-1", "a_1"}) {
		t.Fatal("binary pagination", ids)
	}
	for _, id := range []string{"missing", "hidden"} {
		rows, err := s.ReadGradingInspection(ctx, store.GradingInspectionQuery{JobID: id, Limit: 1})
		if err != nil || len(rows) != 0 {
			t.Fatal(id, rows, err)
		}
	}
	rows, err := s.ReadGradingInspection(ctx, store.GradingInspectionQuery{JobID: "a", Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].Job.ID != "a" {
		t.Fatal("exact", rows, err)
	}
	rows[0].Job.TargetRef = "changed"
	again, err := s.ReadGradingInspection(ctx, store.GradingInspectionQuery{JobID: "a", Limit: 1})
	if err != nil || again[0].Job.TargetRef != "private legacy target" {
		t.Fatal("aliased read", err)
	}
	for _, q := range []store.GradingInspectionQuery{{Limit: 0}, {Limit: 102}, {JobID: "a", After: "b", Limit: 1}, {After: "bad/path", Limit: 1}, {JobID: strings.Repeat("a", 257), Limit: 1}} {
		if _, err := s.ReadGradingInspection(ctx, q); err == nil {
			t.Fatal("unbounded or malformed query", q)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ReadGradingInspection(cancelled, store.GradingInspectionQuery{Limit: 1}); err == nil {
		t.Fatal("cancelled read")
	}
}

func assertGradingInspection(t *testing.T, s store.Store, state string, run, grade bool) {
	t.Helper()
	rows, err := s.ReadGradingInspection(context.Background(), store.GradingInspectionQuery{JobID: "j", Limit: 1})
	if err != nil || len(rows) != 1 {
		t.Fatal("inspection", rows, err)
	}
	r := rows[0]
	if !r.HasDispatch || r.Dispatch.State != state || r.RunPresent != run || r.GradePresent != grade {
		t.Fatal("wrong inspection metadata", r)
	}
	if run && (r.RunSubmissionID != "s" || r.RunRunner != "fake") {
		t.Fatal("wrong run link")
	}
	if grade && (r.GradeSubmissionID != "s" || r.GradeRunID != "run") {
		t.Fatal("wrong grade link")
	}
	if state == store.DispatchPublished && r.Job.Status != store.JobSucceeded {
		t.Fatal("queue not atomically published")
	}
}
