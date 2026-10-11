// Package derivatives owns durable, local image derivatives. Reads never enqueue
// work and workers never fetch the public /d endpoint of this application.
package derivatives

import (
	"errors"
	"time"
)

// The long model names deliberately use GORM's naming strategy (including the
// application's configured table prefix), rather than hard-coded TableName methods.
type DerivativeJob struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	Path            string    `json:"path" gorm:"type:text"`
	Recursive       bool      `json:"recursive"`
	Profiles        string    `json:"profiles" gorm:"size:32"`
	Force           bool      `json:"force"`
	Creator         uint      `json:"creator"`
	RuleID          uint      `json:"rule_id" gorm:"index"`
	StorageID       uint      `json:"storage_id"`
	Binding         string    `json:"binding" gorm:"size:64"`
	State           string    `json:"state" gorm:"size:32;index"`
	Error           string    `json:"error" gorm:"type:text"`
	PauseReason     string    `json:"pause_reason" gorm:"size:64"`
	ScanComplete    bool      `json:"scan_complete"`
	Excludes        string    `json:"-" gorm:"type:text"`
	Fingerprint     string    `json:"-" gorm:"size:64;index"`
	ActiveKey       *string   `json:"-" gorm:"size:64;uniqueIndex"`
	IdempotencyHash *string   `json:"-" gorm:"size:64;uniqueIndex"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Stats           Stats     `gorm:"-" json:"stats"`
}
type Job = DerivativeJob

type JobRequest struct {
	Path           string `json:"path"`
	Recursive      bool   `json:"recursive"`
	Profiles       string `json:"profiles"`
	Force          bool   `json:"force"`
	IdempotencyKey string `json:"idempotency_key"`
	Creator        uint   `json:"creator"`
	RuleID         uint   `json:"rule_id"`
}

type DerivativeItem struct {
	ID              uint                    `json:"id" gorm:"primaryKey"`
	JobID           uint                    `json:"job_id" gorm:"uniqueIndex:derivative_item_path;index"`
	PathKey         string                  `json:"-" gorm:"size:64;uniqueIndex:derivative_item_path"`
	Path            string                  `json:"path" gorm:"type:text"`
	Parent          string                  `json:"-" gorm:"type:text"`
	Kind            string                  `json:"kind" gorm:"size:16"`
	State           string                  `json:"state" gorm:"size:32;index"`
	Stage           string                  `json:"stage" gorm:"size:32"`
	Error           string                  `json:"error" gorm:"type:text"`
	Profiles        string                  `json:"profiles" gorm:"size:32"`
	Revision        string                  `json:"revision" gorm:"size:64"`
	Depth           int                     `json:"depth"`
	Attempts        int                     `json:"attempts"`
	NextAttempt     time.Time               `json:"next_attempt" gorm:"index"`
	LeaseUntil      time.Time               `json:"lease_until"`
	Eligible        bool                    `json:"eligible"`
	DownloadedBytes int64                   `json:"downloaded_bytes"`
	GeneratedBytes  int64                   `json:"generated_bytes"`
	Result          []DerivativeItemProfile `json:"result" gorm:"foreignKey:ItemID"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
}
type Item = DerivativeItem

type DerivativeItemProfile struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	ItemID     uint      `json:"-" gorm:"uniqueIndex:derivative_item_profile;index"`
	Profile    string    `json:"profile" gorm:"size:16;uniqueIndex:derivative_item_profile"`
	State      string    `json:"state" gorm:"size:32"`
	ArtifactID string    `json:"artifact_id,omitempty" gorm:"size:64"`
	Attempts   int       `json:"attempts"`
	Error      string    `json:"error,omitempty" gorm:"type:text"`
	Bytes      int64     `json:"bytes"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type DerivativeArtifact struct {
	ID             string     `json:"id" gorm:"size:64;primaryKey"`
	SourceKey      string     `json:"-" gorm:"size:64;index"`
	PathKey        string     `json:"-" gorm:"size:64;index"`
	ParentKey      string     `json:"-" gorm:"size:64;index"`
	StorageID      uint       `json:"storage_id" gorm:"index"`
	Binding        string     `json:"binding" gorm:"size:64"`
	Path           string     `json:"path" gorm:"type:text"`
	Revision       string     `json:"revision" gorm:"size:64"`
	Profile        string     `json:"profile" gorm:"size:16"`
	Version        int        `json:"version"`
	State          string     `json:"state" gorm:"size:32;index"`
	File           string     `json:"-" gorm:"type:text"`
	OutputTargetID uint       `json:"-" gorm:"index;default:0"`
	ObjectKey      string     `json:"-" gorm:"type:text"`
	ObjectID       string     `json:"-" gorm:"size:64"` // Original publication ID for retained S3 keys.
	Width          int        `json:"width"`
	Height         int        `json:"height"`
	Bytes          int64      `json:"bytes"`
	SHA256         string     `json:"sha256" gorm:"size:64"`
	Error          string     `json:"error,omitempty" gorm:"type:text"`
	Manual         bool       `json:"manual"`
	AccessedAt     *time.Time `json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
type Artifact = DerivativeArtifact

// Sources preserve the latest locally confirmed revision. A missing directory
// listing or an offline storage never means its sources have been deleted.
type DerivativeSource struct {
	Key        string `gorm:"column:source_key;size:64;primaryKey"`
	PathKey    string `gorm:"size:64;index"`
	ParentKey  string `gorm:"size:64;index"`
	StorageID  uint   `gorm:"index"`
	Binding    string `gorm:"size:64"`
	Path       string `gorm:"type:text"`
	Revision   string `gorm:"size:64"`
	Deleted    bool
	ObservedAt time.Time
	UpdatedAt  time.Time
}

type DerivativeRule struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	Name            string    `json:"name" gorm:"size:128"`
	Path            string    `json:"path" gorm:"type:text"`
	Recursive       bool      `json:"recursive"`
	Profiles        string    `json:"profiles" gorm:"size:32"`
	Enabled         bool      `json:"enabled"`
	IntervalMinutes int       `json:"interval_minutes"`
	Excludes        string    `json:"excludes" gorm:"type:text"`
	Creator         uint      `json:"creator"`
	StorageID       uint      `json:"storage_id"`
	Binding         string    `json:"binding" gorm:"size:64"`
	LastRun         time.Time `json:"last_run"`
	NextRun         time.Time `json:"next_run" gorm:"index"`
	LastState       string    `json:"last_state" gorm:"size:32"`
	PauseReason     string    `json:"pause_reason" gorm:"size:64"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type Rule = DerivativeRule

type Stats struct {
	Discovered      int64 `json:"discovered"`
	Eligible        int64 `json:"eligible"`
	ScannedDirs     int64 `json:"scanned_dirs"`
	FailedDirs      int64 `json:"failed_dirs"`
	CompletedFiles  int64 `json:"completed_files"`
	PartialFiles    int64 `json:"partial_files"`
	ExistingFiles   int64 `json:"existing_files"`
	SkippedFiles    int64 `json:"skipped_files"`
	FailedFiles     int64 `json:"failed_files"`
	PendingFiles    int64 `json:"pending_files"`
	RetryWait       int64 `json:"retry_wait"`
	ActiveFiles     int64 `json:"active_files"`
	DownloadedBytes int64 `json:"downloaded_bytes"`
	GeneratedBytes  int64 `json:"generated_bytes"`
	ArtifactsReady  int64 `json:"artifacts_ready"`
	ArtifactsFailed int64 `json:"artifacts_failed"`
}

var (
	ErrDisabled           = errors.New("derivatives_disabled")
	ErrClosed             = errors.New("derivatives_closed")
	ErrInvalidPath        = errors.New("invalid_derivative_path")
	ErrInvalidProfiles    = errors.New("invalid_derivative_profiles")
	ErrBinding            = errors.New("storage_binding_changed")
	ErrUnsupportedStorage = errors.New("unsupported_derivative_storage")
	ErrPermission         = errors.New("creator_not_active_admin")
	ErrLimit              = errors.New("derivative_queue_limit")
	ErrDisk               = errors.New("derivative_disk_protection")
	ErrSourceChanged      = errors.New("source_changed")
	ErrConflict           = errors.New("derivative_request_conflict")
)

var activeStates = []string{"queued", "scanning", "processing", "retry_wait", "paused"}
var runnableStates = []string{"queued", "scanning", "processing", "retry_wait"}
var pendingStates = []string{"queued", "retry_wait", "active"}

const maxDownload int64 = 128 << 20
const workReserve int64 = maxDownload + (64 << 20)
