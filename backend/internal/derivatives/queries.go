package derivatives

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

// MaxSearchLength bounds an admin list search to 256 Unicode code points. The
// byte check also bounds validation work for malformed or oversized input.
const MaxSearchLength = 256

var (
	ErrInvalidFilterState = errors.New("invalid_derivative_state")
	ErrInvalidSearch      = errors.New("invalid_derivative_search")
)

// List categories deliberately differ from the worker's activeStates, which
// includes paused jobs for deduplication/control. Here categories are disjoint:
// active = queued/scanning/processing/retry_wait/cancelling; paused = paused;
// attention = partial_failed/failed/completed_with_errors; completed = completed;
// cancelled = cancelled. Empty state and "all" mean no state predicate.
var jobListGroups = map[string][]string{
	"active":    {"queued", "scanning", "processing", "retry_wait", "cancelling"},
	"paused":    {"paused"},
	"attention": {"partial_failed", "failed", "completed_with_errors"},
	"completed": {"completed"},
	"cancelled": {"cancelled"},
}

// Item categories use item states, not profile states or stages. "active" is
// exactly the active item state; pending excludes active claims, and attention
// includes both complete and partial item failures. Other valid item states may
// be requested individually. Pausing a job does not create a paused item state.
var itemListGroups = map[string][]string{
	"attention": {"failed", "partial_failed"},
	"pending":   {"queued", "retry_wait"},
	"active":    {"active"},
}

func listStates(state string, groups map[string][]string, exact []string) ([]string, error) {
	if state == "" || state == "all" {
		return nil, nil
	}
	if states, ok := groups[state]; ok {
		return states, nil
	}
	if contains(exact, state) {
		return []string{state}, nil
	}
	return nil, ErrInvalidFilterState
}

func literalSearchPattern(search string) (string, error) {
	if len(search) > utf8.UTFMax*MaxSearchLength || !utf8.ValidString(search) || utf8.RuneCountInString(search) > MaxSearchLength {
		return "", ErrInvalidSearch
	}
	if search == "" {
		return "", nil
	}
	// Use an explicit, portable escape character, including escaping the escape
	// character itself. A filename's %, _ or ! must never broaden the query.
	escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search)
	return "%" + escaped + "%", nil
}

func numericSearch(search string) bool {
	for _, c := range search {
		if c < '0' || c > '9' {
			return false
		}
	}
	return search != ""
}

func jobListQuery(database *gorm.DB, state, search string) (*gorm.DB, error) {
	states, err := listStates(state, jobListGroups, []string{
		"queued", "scanning", "processing", "retry_wait", "cancelling",
		"partial_failed", "failed", "completed_with_errors",
	})
	if err != nil {
		return nil, err
	}
	pattern, err := literalSearchPattern(search)
	if err != nil {
		return nil, err
	}
	q := database.Model(&Job{})
	if len(states) != 0 {
		q = q.Where("state IN ?", states)
	}
	if pattern != "" {
		// Numeric searches retain path substring semantics and additionally match
		// only that exact job ID (never an ID substring or SQL cast).
		if id, err := strconv.ParseUint(search, 10, 63); err == nil && id != 0 && numericSearch(search) {
			q = q.Where("(path LIKE ? ESCAPE '!' OR id = ?)", pattern, id)
		} else {
			q = q.Where("path LIKE ? ESCAPE '!'", pattern)
		}
	}
	return q, nil
}

func itemListQuery(database *gorm.DB, id uint, state, search string) (*gorm.DB, error) {
	states, err := listStates(state, itemListGroups, []string{
		"queued", "retry_wait", "succeeded", "existing", "skipped",
		"failed", "partial_failed", "cancelled",
	})
	if err != nil {
		return nil, err
	}
	pattern, err := literalSearchPattern(search)
	if err != nil {
		return nil, err
	}
	q := database.Model(&Item{}).Where("job_id = ?", id)
	if len(states) != 0 {
		q = q.Where("state IN ?", states)
	}
	if pattern != "" {
		// Keep all filtering in SQL before pagination. IN avoids duplicate item
		// rows/counts when both profiles match, and all table names are derived
		// by GORM so configured prefixes/naming strategies remain intact. Scope
		// profile-error searches to this job as well as the outer item query.
		jobItems := database.Model(&Item{}).Select("id").Where("job_id = ?", id)
		profileErrors := database.Model(&DerivativeItemProfile{}).Select("item_id").
			Where("item_id IN (?)", jobItems).Where("error LIKE ? ESCAPE '!'", pattern)
		q = q.Where("(path LIKE ? ESCAPE '!' OR error LIKE ? ESCAPE '!' OR stage LIKE ? ESCAPE '!' OR kind LIKE ? ESCAPE '!' OR id IN (?))",
			pattern, pattern, pattern, pattern, profileErrors)
	}
	return q, nil
}

// JobSummary is global, independent of list pagination/search, and counts only
// persisted job states. It does not load jobs or aggregate their item statistics.
// The categories match jobListGroups; an unknown future state contributes to
// Total only until its category is explicitly defined.
type JobSummary struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Paused    int64 `json:"paused"`
	Attention int64 `json:"attention"`
	Completed int64 `json:"completed"`
	Cancelled int64 `json:"cancelled"`
}

func (m *Manager) JobsSummary() (JobSummary, error) {
	var summary JobSummary
	// Return a single aggregate row, including zeroes for an empty job table.
	err := m.db.Model(&Job{}).Select(
		"COUNT(*) AS total, "+
			"COALESCE(SUM(CASE WHEN state IN ? THEN 1 ELSE 0 END), 0) AS active, "+
			"COALESCE(SUM(CASE WHEN state IN ? THEN 1 ELSE 0 END), 0) AS paused, "+
			"COALESCE(SUM(CASE WHEN state IN ? THEN 1 ELSE 0 END), 0) AS attention, "+
			"COALESCE(SUM(CASE WHEN state IN ? THEN 1 ELSE 0 END), 0) AS completed, "+
			"COALESCE(SUM(CASE WHEN state IN ? THEN 1 ELSE 0 END), 0) AS cancelled",
		jobListGroups["active"], jobListGroups["paused"], jobListGroups["attention"],
		jobListGroups["completed"], jobListGroups["cancelled"],
	).Scan(&summary).Error
	return summary, err
}
