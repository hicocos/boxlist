package derivatives

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func queryJob(t *testing.T, m *Manager, path, state string) Job {
	t.Helper()
	j := Job{Path: path, State: state, Profiles: "thumb,reader", Creator: 1}
	if err := m.db.Create(&j).Error; err != nil {
		t.Fatal(err)
	}
	return j
}

func queryItem(t *testing.T, m *Manager, j Job, path, state string) Item {
	t.Helper()
	item := coreItem(t, m, j, path)
	if err := m.db.Model(&item).Update("state", state).Error; err != nil {
		t.Fatal(err)
	}
	item.State = state
	return item
}

func TestJobsFilteredSearchBeforePagination(t *testing.T) {
	m := coreManager(t)
	old := queryJob(t, m, "/pictures/needle/old", "completed")
	middle := queryJob(t, m, "/pictures/needle/middle", "failed")
	newest := queryJob(t, m, "/pictures/other", "completed")
	queryItem(t, m, old, "/pictures/needle/old/a.png", "succeeded")

	rows, total, err := m.Jobs(1, 1)
	if err != nil || total != 3 || len(rows) != 1 || rows[0].ID != newest.ID {
		t.Fatalf("unfiltered wrapper: rows=%v total=%d err=%v", rows, total, err)
	}
	for _, tc := range []struct {
		page  int
		state string
		want  uint
		total int64
	}{
		{1, "", middle.ID, 2},
		{2, "all", old.ID, 2},
		{3, "", 0, 2},
		{1, "completed", old.ID, 1},
		{1, "attention", middle.ID, 1},
		{1, "active", 0, 0},
	} {
		rows, total, err = m.JobsFiltered(tc.page, 1, tc.state, "needle")
		if err != nil || total != tc.total {
			t.Fatalf("page=%d state=%q total=%d err=%v", tc.page, tc.state, total, err)
		}
		if tc.want == 0 {
			if len(rows) != 0 {
				t.Fatalf("expected empty page: %v", rows)
			}
			continue
		}
		if len(rows) != 1 || rows[0].ID != tc.want {
			t.Fatalf("page=%d state=%q rows=%v", tc.page, tc.state, rows)
		}
		if tc.want == old.ID && rows[0].Stats.CompletedFiles != 1 {
			t.Fatalf("page statistics lost: %+v", rows[0].Stats)
		}
	}
}

func TestJobsFilteredLiteralSearchAndExactID(t *testing.T) {
	m := coreManager(t)
	for _, j := range []Job{
		{ID: 1, Path: "/pictures/target", State: "completed"},
		{ID: 11, Path: "/pictures/other", State: "completed"},
		{ID: 12, Path: "/pictures/chapter1", State: "failed"},
	} {
		if err := m.db.Create(&j).Error; err != nil {
			t.Fatal(err)
		}
	}
	percent := queryJob(t, m, "/pictures/100%", "completed")
	underscore := queryJob(t, m, "/pictures/chapter_name", "completed")
	bang := queryJob(t, m, "/pictures/!cover", "completed")
	distractor := queryJob(t, m, "/pictures/100x/chapter-name", "completed")
	for _, tc := range []struct {
		search, state string
		want          []uint
	}{
		{"%", "", []uint{percent.ID}},
		{"_", "", []uint{underscore.ID}},
		{"!", "", []uint{bang.ID}},
		{"1", "", []uint{distractor.ID, percent.ID, 12, 1}}, // Path substrings plus exact ID, not ID 11.
		{"1", "failed", []uint{12}},
		{"0001", "", []uint{1}},
		{"+1", "", []uint{}},
		{"999999999999999999999999", "", []uint{}},
		{"%' OR 1=1 --", "", []uint{}},
	} {
		rows, total, err := m.JobsFiltered(1, 20, tc.state, tc.search)
		ids := []uint{}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if err != nil || total != int64(len(tc.want)) || !reflect.DeepEqual(ids, tc.want) {
			t.Fatalf("search=%q state=%q ids=%v total=%d err=%v want=%v", tc.search, tc.state, ids, total, err, tc.want)
		}
	}
}

func TestJobListGroupsAndGlobalSummary(t *testing.T) {
	m := coreManager(t)
	if summary, err := m.JobsSummary(); err != nil || summary != (JobSummary{}) {
		t.Fatalf("empty summary=%+v err=%v", summary, err)
	}
	states := []string{"queued", "scanning", "processing", "retry_wait", "cancelling", "paused", "partial_failed", "failed", "completed_with_errors", "completed", "cancelled"}
	for _, state := range states {
		queryJob(t, m, "/pictures/"+state, state)
	}
	for _, tc := range []struct {
		state string
		count int64
	}{
		{"", 11}, {"all", 11}, {"active", 5}, {"paused", 1},
		{"attention", 3}, {"completed", 1}, {"cancelled", 1},
	} {
		rows, total, err := m.JobsFiltered(1, 2, tc.state, "")
		if err != nil || total != tc.count || int64(len(rows)) > tc.count || len(rows) > 2 {
			t.Fatalf("group=%q total=%d rows=%v err=%v", tc.state, total, rows, err)
		}
	}
	for _, state := range states {
		rows, total, err := m.JobsFiltered(1, 10, state, "")
		if err != nil || total != 1 || len(rows) != 1 || rows[0].State != state {
			t.Fatalf("exact state=%q total=%d rows=%v err=%v", state, total, rows, err)
		}
	}

	// Prove summary reads only the job table: poison query/row callbacks for
	// item/profile tables. Job count categories must not call m.stats.
	const callback = "test:summary_job_states_only"
	guard := func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name != "DerivativeJob" {
			tx.AddError(errors.New("unexpected non-job summary query"))
		}
	}
	if err := m.db.Callback().Query().Before("gorm:query").Register(callback, guard); err != nil {
		t.Fatal(err)
	}
	if err := m.db.Callback().Row().Before("gorm:row").Register(callback, guard); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.db.Callback().Query().Remove(callback)
		m.db.Callback().Row().Remove(callback)
	})
	want := JobSummary{Total: 11, Active: 5, Paused: 1, Attention: 3, Completed: 1, Cancelled: 1}
	if summary, err := m.JobsSummary(); err != nil || summary != want {
		t.Fatalf("summary=%+v err=%v want=%+v", summary, err, want)
	}
}

func TestItemsFilteredGroupsAndSearchBeforePagination(t *testing.T) {
	m := coreManager(t)
	job := queryJob(t, m, "/pictures", "partial_failed")
	id := strconv.FormatUint(uint64(job.ID), 10)
	states := []string{"succeeded", "existing", "skipped", "cancelled", "queued", "retry_wait", "active", "failed", "partial_failed"}
	for _, state := range states {
		queryItem(t, m, job, "/pictures/"+state+".png", state)
	}
	for _, tc := range []struct {
		state string
		count int64
	}{
		{"", 9}, {"all", 9}, {"attention", 2}, {"pending", 2}, {"active", 1},
	} {
		rows, total, err := m.ItemsFiltered(id, 1, 1, tc.state, "")
		if err != nil || total != tc.count || len(rows) != 1 {
			t.Fatalf("group=%q total=%d rows=%v err=%v", tc.state, total, rows, err)
		}
	}
	for _, state := range states {
		rows, total, err := m.Items(id, 1, 10, state)
		if err != nil || total != 1 || len(rows) != 1 || rows[0].State != state {
			t.Fatalf("wrapper exact state=%q total=%d rows=%v err=%v", state, total, rows, err)
		}
	}
	for page := 1; page <= 3; page++ {
		rows, total, err := m.ItemsFiltered(id, page, 1, "attention", "failed")
		wantLength := 1
		if page == 3 {
			wantLength = 0
		}
		if err != nil || total != 2 || len(rows) != wantLength {
			t.Fatalf("attention search page=%d total=%d rows=%v err=%v", page, total, rows, err)
		}
	}
}

func TestItemsFilteredSearchFieldsResultsAndJobScope(t *testing.T) {
	m := coreManager(t)
	job := queryJob(t, m, "/pictures", "completed")
	id := strconv.FormatUint(uint64(job.ID), 10)
	queryItem(t, m, job, "/pictures/first.png", "succeeded")
	pathMatch := queryItem(t, m, job, "/pictures/path-only.png", "failed")
	errorMatch := queryItem(t, m, job, "/pictures/error.png", "failed")
	stageMatch := queryItem(t, m, job, "/pictures/stage.png", "active")
	kindMatch := queryItem(t, m, job, "/pictures/directory", "failed")
	profileMatch := queryItem(t, m, job, "/pictures/result.png", "partial_failed")
	for _, update := range []struct {
		item   Item
		fields map[string]any
	}{
		{errorMatch, map[string]any{"error": "item-only"}},
		{stageMatch, map[string]any{"stage": "publishing"}},
		{kindMatch, map[string]any{"kind": "directory"}},
		{profileMatch, map[string]any{"attempts": 3, "error": ""}},
	} {
		if err := m.db.Model(&Item{}).Where("id = ?", update.item.ID).Updates(update.fields).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := m.db.Model(&DerivativeItemProfile{}).Where("item_id = ?", profileMatch.ID).
		Updates(map[string]any{"state": "failed", "error": "profile-only", "attempts": 2}).Error; err != nil {
		t.Fatal(err)
	}
	otherJob := queryJob(t, m, "/other", "failed")
	otherItem := queryItem(t, m, otherJob, "/other/path-only.png", "failed")
	if err := m.db.Model(&DerivativeItemProfile{}).Where("item_id = ?", otherItem.ID).Update("error", "profile-only").Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		search string
		want   uint
	}{
		{"path-only", pathMatch.ID}, {"item-only", errorMatch.ID},
		{"publishing", stageMatch.ID}, {"directory", kindMatch.ID},
		{"profile-only", profileMatch.ID},
	} {
		rows, total, err := m.ItemsFiltered(id, 1, 1, "", tc.search)
		if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != tc.want {
			t.Fatalf("search=%q total=%d rows=%v err=%v", tc.search, total, rows, err)
		}
		if tc.want == profileMatch.ID {
			if rows[0].Attempts != 3 || len(rows[0].Result) != 2 {
				t.Fatalf("item attempts/results lost: %+v", rows[0])
			}
			for _, result := range rows[0].Result {
				if result.Attempts != 2 || result.Error != "profile-only" || result.State != "failed" {
					t.Fatalf("profile result lost: %+v", result)
				}
			}
		}
	}
	rows, total, err := m.ItemsFiltered(id, 1, 10, "pending", "profile-only")
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("profile subquery escaped state predicate: total=%d rows=%v err=%v", total, rows, err)
	}
}

func TestItemsFilteredLiteralSearch(t *testing.T) {
	m := coreManager(t)
	job := queryJob(t, m, "/pictures", "failed")
	id := strconv.FormatUint(uint64(job.ID), 10)
	percent := queryItem(t, m, job, "/pictures/100%.png", "failed")
	underscore := queryItem(t, m, job, "/pictures/chapter_name.png", "failed")
	bang := queryItem(t, m, job, "/pictures/!cover.png", "failed")
	queryItem(t, m, job, "/pictures/100x/chapter-name.png", "failed")
	profile := queryItem(t, m, job, "/pictures/profile.png", "failed")
	if err := m.db.Model(&DerivativeItemProfile{}).Where("item_id = ?", profile.ID).Update("error", "profile%_!").Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		search string
		want   []uint
	}{
		{"%", []uint{percent.ID, profile.ID}},
		{"_", []uint{underscore.ID, profile.ID}},
		{"!", []uint{bang.ID, profile.ID}},
		{"%_!", []uint{profile.ID}},
		{"%' OR 1=1 --", []uint{}},
	} {
		rows, total, err := m.ItemsFiltered(id, 1, 10, "attention", tc.search)
		ids := []uint{}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if err != nil || total != int64(len(tc.want)) || !reflect.DeepEqual(ids, tc.want) {
			t.Fatalf("search=%q ids=%v total=%d err=%v want=%v", tc.search, ids, total, err, tc.want)
		}
	}
}

func TestListFiltersRejectInvalidAndBoundSearch(t *testing.T) {
	m := coreManager(t)
	job := queryJob(t, m, "/pictures", "queued")
	id := strconv.FormatUint(uint64(job.ID), 10)
	for _, state := range []string{"unknown", "ACTIVE", " active ", "state' OR 1=1"} {
		if _, _, err := m.JobsFiltered(1, 10, state, ""); !errors.Is(err, ErrInvalidFilterState) {
			t.Errorf("job state=%q err=%v", state, err)
		}
		if _, _, err := m.ItemsFiltered(id, 1, 10, state, ""); !errors.Is(err, ErrInvalidFilterState) {
			t.Errorf("item state=%q err=%v", state, err)
		}
	}
	for _, search := range []string{strings.Repeat("x", MaxSearchLength+1), strings.Repeat("图", MaxSearchLength+1), string([]byte{0xff})} {
		if _, _, err := m.JobsFiltered(1, 10, "", search); !errors.Is(err, ErrInvalidSearch) {
			t.Errorf("job search length=%d err=%v", len(search), err)
		}
		if _, _, err := m.ItemsFiltered(id, 1, 10, "", search); !errors.Is(err, ErrInvalidSearch) {
			t.Errorf("item search length=%d err=%v", len(search), err)
		}
	}
	for _, search := range []string{strings.Repeat("x", MaxSearchLength), strings.Repeat("图", MaxSearchLength)} {
		if _, _, err := m.JobsFiltered(1, 10, "", search); err != nil {
			t.Errorf("valid job search: %v", err)
		}
		if _, _, err := m.ItemsFiltered(id, 1, 10, "", search); err != nil {
			t.Errorf("valid item search: %v", err)
		}
	}
	if _, _, err := m.ItemsFiltered("999999", 1, 10, "", ""); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing job: %v", err)
	}
}

func TestJobsFilteredRetainsBoundedPagination(t *testing.T) {
	m := coreManager(t)
	// Stay below the manager's existing 200-row hard cap plus a single overflow
	// row; use one bounded insert, not a large/unbounded fixture or worker scan.
	jobs := make([]Job, 201)
	for i := range jobs {
		jobs[i] = Job{Path: fmt.Sprintf("/pictures/%03d", i), State: "completed"}
	}
	if err := m.db.Create(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	q, err := jobListQuery(m.db, "completed", "")
	if err != nil {
		t.Fatal(err)
	}
	offset, limit := pageBounds(1, 10000)
	// Exercise the same SQL page bounds without repeatedly aggregating stats for
	// 200 empty jobs; the small manager tests above cover the stats contract.
	rows := []Job{}
	if err = q.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil || len(rows) != 200 {
		t.Fatalf("bounded query rows=%d err=%v", len(rows), err)
	}
}
