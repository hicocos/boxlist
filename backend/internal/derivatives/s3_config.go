package derivatives

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Identity fields are immutable. Credentials and explicit CDN metadata may rotate;
// switching destinations retains old targets for reads and exact-key cleanup.
// Do not add TableName methods: the application's GORM prefix must apply.
type DerivativeS3Target struct {
	ID           uint      `gorm:"primaryKey" json:"-"`
	IdentityHash string    `gorm:"size:64;uniqueIndex" json:"-"`
	Provider     string    `gorm:"size:16" json:"-"`
	Endpoint     string    `gorm:"type:text" json:"-"`
	Bucket       string    `gorm:"size:63" json:"-"`
	Region       string    `gorm:"size:64" json:"-"`
	Prefix       string    `gorm:"size:512" json:"-"`
	PathStyle    bool      `json:"-"`
	CDNURL       string    `gorm:"type:text" json:"-"`
	PublicCDN    bool      `json:"-"`
	Credentials  []byte    `json:"-"`
	CreatedAt    time.Time `json:"-"`
	UpdatedAt    time.Time `json:"-"`
	// Probe-only credentials never enter GORM or API serialization.
	accessKey string `gorm:"-" json:"-"`
	secretKey string `gorm:"-" json:"-"`
}

type DerivativeS3Setting struct {
	ID        uint `gorm:"primaryKey;autoIncrement:false"`
	Enabled   bool
	TargetID  uint
	UpdatedAt time.Time
}

type S3ConfigRequest struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider,omitempty"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	PathStyle bool   `json:"path_style"`
	CDNURL    string `json:"cdn_url"`
	PublicCDN bool   `json:"public_cdn"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

type S3ConfigView struct {
	Enabled             bool   `json:"enabled"`
	Provider            string `json:"provider"`
	Endpoint            string `json:"endpoint"`
	Region              string `json:"region"`
	Bucket              string `json:"bucket"`
	Prefix              string `json:"prefix"`
	PathStyle           bool   `json:"path_style"`
	CDNURL              string `json:"cdn_url"`
	PublicCDN           bool   `json:"public_cdn"`
	AccessKeyConfigured bool   `json:"access_key_configured"`
	SecretKeyConfigured bool   `json:"secret_key_configured"`
	TargetID            uint   `json:"target_id"`
	ObjectCount         int64  `json:"object_count"`
	UsedBytes           int64  `json:"used_bytes"`
}

type S3TestResult struct {
	Success bool `json:"success"`
	Skipped bool `json:"skipped"`
}

// S3Error has no underlying provider error, endpoint, signed URL or credential.
type S3Error struct{ Code string }

func (e *S3Error) Error() string { return e.Code }

var (
	ErrS3ConfigurationInvalid = &S3Error{Code: "s3_configuration_invalid"}
	ErrS3CredentialsRequired  = &S3Error{Code: "s3_credentials_required"}
	ErrS3ConfigurationFailed  = &S3Error{Code: "s3_configuration_failed"}
	ErrS3KeyUnavailable       = &S3Error{Code: "s3_key_unavailable"}
	ErrS3TargetUnavailable    = &S3Error{Code: "s3_target_unavailable"}
	ErrS3UploadFailed         = &S3Error{Code: "s3_upload_failed"}
	ErrS3ReadFailed           = &S3Error{Code: "s3_read_failed"}
	ErrS3ObjectMissing        = &S3Error{Code: "s3_object_missing"}
	ErrS3DeleteFailed         = &S3Error{Code: "s3_delete_failed"}
	ErrS3ProbeFailed          = &S3Error{Code: "s3_probe_failed"}
	ErrS3CDNProbeFailed       = &S3Error{Code: "s3_cdn_probe_failed"}
	ErrS3ChecksumMismatch     = &S3Error{Code: "s3_checksum_mismatch"}
)

// Parent ErrorCode must consult this before its generic driver error branches.
func S3ErrorCode(err error) (string, bool) {
	var e *S3Error
	if !errors.As(err, &e) {
		return "", false
	}
	switch e.Code {
	case "s3_configuration_invalid", "s3_credentials_required", "s3_configuration_failed", "s3_key_unavailable", "s3_target_unavailable", "s3_upload_failed", "s3_read_failed", "s3_object_missing", "s3_delete_failed", "s3_probe_failed", "s3_cdn_probe_failed", "s3_checksum_mismatch":
		return e.Code, true
	}
	return "s3_configuration_failed", true
}

// Call once from New AFTER the existing artifact migration, before recover.
// A lost key with any existing target is fatal; never silently replace it.
func (m *Manager) initS3Configuration() error {
	if err := m.db.AutoMigrate(&DerivativeS3Target{}, &DerivativeS3Setting{}); err != nil {
		return ErrS3ConfigurationFailed
	}
	var count int64
	if err := m.db.Model(&DerivativeS3Target{}).Count(&count).Error; err != nil {
		return ErrS3ConfigurationFailed
	}
	if count > 0 {
		if _, err := m.s3EncryptionKey(false); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) s3Setting(ctx context.Context) (DerivativeS3Setting, error) {
	var s DerivativeS3Setting
	err := m.db.WithContext(ctx).First(&s, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DerivativeS3Setting{ID: 1}, nil
	}
	if err != nil {
		return s, ErrS3ConfigurationFailed
	}
	return s, nil
}

func (m *Manager) s3LoadTarget(ctx context.Context, id uint) (*DerivativeS3Target, error) {
	var t DerivativeS3Target
	if id == 0 {
		return nil, ErrS3TargetUnavailable
	}
	if err := m.db.WithContext(ctx).First(&t, id).Error; err != nil {
		return nil, ErrS3TargetUnavailable
	}
	return &t, nil
}

// Remote accounting includes all retained targets, not only the active one.
// It uses the artifact ledger, never bucket listing or unrelated bucket objects.
func (m *Manager) S3Configuration() (S3ConfigView, error) {
	s, err := m.s3Setting(context.Background())
	if err != nil {
		return S3ConfigView{}, err
	}
	view := S3ConfigView{Enabled: s.Enabled, TargetID: s.TargetID, Provider: "s3"}
	if s.TargetID != 0 {
		t, err := m.s3LoadTarget(context.Background(), s.TargetID)
		if err != nil {
			return view, err
		}
		if _, _, err = m.s3Credentials(t); err != nil {
			return view, err
		}
		view.Endpoint, view.Bucket, view.Region, view.Prefix, view.PathStyle = t.Endpoint, t.Bucket, t.Region, t.Prefix, t.PathStyle
		view.AccessKeyConfigured, view.SecretKeyConfigured = true, true
		view.CDNURL, view.PublicCDN = t.CDNURL, t.PublicCDN
		if t.Provider != "" {
			view.Provider = t.Provider
		}
	}
	var usage struct {
		ObjectCount int64
		UsedBytes   int64
	}
	if err := m.db.Model(&Artifact{}).Where("output_target_id > 0").Select("COUNT(*) AS object_count, COALESCE(SUM(bytes), 0) AS used_bytes").Scan(&usage).Error; err != nil {
		return view, ErrS3ConfigurationFailed
	}
	view.ObjectCount, view.UsedBytes = usage.ObjectCount, usage.UsedBytes
	return view, nil
}

func s3Identity(t *DerivativeS3Target) string {
	identity := []any{t.Endpoint, t.Bucket, t.Region, t.Prefix, t.PathStyle}
	// Preserve existing S3 identities and encrypted credential bindings.
	if t.Provider == "r2" {
		identity = append(identity, t.Provider)
	}
	b, _ := json.Marshal(identity)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (m *Manager) s3Candidate(ctx context.Context, request S3ConfigRequest) (*DerivativeS3Target, error) {
	t, err := normalizeS3Target(request)
	if err != nil {
		return nil, err
	}
	t.IdentityHash = s3Identity(t)
	access, secret := request.AccessKey, request.SecretKey
	if access == "" || secret == "" {
		s, err := m.s3Setting(ctx)
		if err != nil {
			return nil, err
		}
		if s.TargetID == 0 {
			return nil, ErrS3CredentialsRequired
		}
		old, err := m.s3LoadTarget(ctx, s.TargetID)
		if err != nil {
			return nil, err
		}
		if old.IdentityHash != t.IdentityHash {
			return nil, ErrS3CredentialsRequired
		}
		oldAccess, oldSecret, err := m.s3Credentials(old)
		if err != nil {
			return nil, err
		}
		if access == "" {
			access = oldAccess
		}
		if secret == "" {
			secret = oldSecret
		}
	}
	if len(access) > 256 || len(secret) > 4096 || strings.ContainsAny(access+secret, "\x00\r\n") || strings.TrimSpace(access) == "" || strings.TrimSpace(secret) == "" {
		return nil, ErrS3CredentialsRequired
	}
	t.accessKey, t.secretKey = access, secret
	// ID stays zero: a probe MUST use the requested credentials, not DB values.
	return t, nil
}

func (m *Manager) SaveS3Configuration(ctx context.Context, request S3ConfigRequest) (S3ConfigView, error) {
	if !request.Enabled {
		// Disable is deliberately non-destructive; all target fields are retained.
		err := m.transaction(ctx, func(tx *gorm.DB) error {
			s := DerivativeS3Setting{ID: 1}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&s).Error; err != nil {
				return err
			}
			return tx.Model(&DerivativeS3Setting{}).Where("id = ?", 1).Update("enabled", false).Error
		})
		if err != nil {
			return S3ConfigView{}, ErrS3ConfigurationFailed
		}
		return m.S3Configuration()
	}
	t, err := m.s3Candidate(ctx, request)
	if err != nil {
		return S3ConfigView{}, err
	}
	// Never rely on a prior UI Test request. Saving runs its own complete probe.
	if err = m.s3Probe(ctx, t); err != nil {
		return S3ConfigView{}, err
	}
	key, err := m.s3EncryptionKey(true)
	if err != nil {
		return S3ConfigView{}, err
	}
	sealed, err := sealS3Credentials(key, t.IdentityHash, t.accessKey, t.secretKey)
	if err != nil {
		return S3ConfigView{}, err
	}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		row := *t
		row.ID, row.Credentials = 0, sealed
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "identity_hash"}}, DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("identity_hash = ?", t.IdentityHash).First(&row).Error; err != nil {
			return err
		}
		if err := tx.Model(&DerivativeS3Target{}).Where("id = ?", row.ID).Updates(map[string]any{"credentials": sealed, "cdn_url": t.CDNURL, "public_cdn": t.PublicCDN, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		s := DerivativeS3Setting{ID: 1, Enabled: true, TargetID: row.ID, UpdatedAt: time.Now()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "target_id", "updated_at"})}).Create(&s).Error
	})
	if err != nil {
		return S3ConfigView{}, ErrS3ConfigurationFailed
	}
	return m.S3Configuration()
}

func (m *Manager) TestS3Configuration(ctx context.Context, request S3ConfigRequest) (S3TestResult, error) {
	if !request.Enabled && request.Endpoint == "" && request.Bucket == "" && request.Region == "" && request.Prefix == "" && request.AccessKey == "" && request.SecretKey == "" && request.CDNURL == "" && !request.PublicCDN {
		return S3TestResult{Success: true, Skipped: true}, nil
	}
	t, err := m.s3Candidate(ctx, request)
	if err != nil {
		return S3TestResult{}, err
	}
	if err = m.s3Probe(ctx, t); err != nil {
		return S3TestResult{}, err
	}
	return S3TestResult{Success: true}, nil
}

func (m *Manager) s3WriteTarget() (*DerivativeS3Target, error) {
	s, err := m.s3Setting(context.Background())
	if err != nil || !s.Enabled {
		return nil, err
	}
	return m.s3LoadTarget(context.Background(), s.TargetID)
}

func (m *Manager) s3TargetForArtifact(a Artifact) (*DerivativeS3Target, error) {
	if a.OutputTargetID == 0 {
		return nil, nil
	}
	// Reading old targets is independent of the setting's current Enabled flag.
	return m.s3LoadTarget(context.Background(), a.OutputTargetID)
}

// Atomic no-overwrite publication avoids partial keys and cross-process races.
// The root must be the private Manager.root, not a configured arbitrary path.
func (m *Manager) s3EncryptionKey(create bool) ([]byte, error) {
	if noSymlinks(m.root) != nil {
		return nil, ErrS3KeyUnavailable
	}
	path := filepath.Join(m.root, "s3.key")
	read := func() ([]byte, error) {
		st, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
			return nil, ErrS3KeyUnavailable
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, ErrS3KeyUnavailable
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil || !os.SameFile(st, opened) {
			return nil, ErrS3KeyUnavailable
		}
		key, err := io.ReadAll(io.LimitReader(f, 33))
		if err != nil || len(key) != 32 {
			return nil, ErrS3KeyUnavailable
		}
		return key, nil
	}
	if key, err := read(); err == nil {
		return key, nil
	} else if !os.IsNotExist(err) || !create {
		return nil, ErrS3KeyUnavailable
	}
	var count int64
	if err := m.db.Model(&DerivativeS3Target{}).Count(&count).Error; err != nil || count != 0 {
		return nil, ErrS3KeyUnavailable
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, ErrS3KeyUnavailable
	}
	f, err := os.CreateTemp(m.root, ".s3-key-*")
	if err != nil {
		return nil, ErrS3KeyUnavailable
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(key)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return nil, ErrS3KeyUnavailable
	}
	if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return nil, ErrS3KeyUnavailable
	}
	dir, err := os.Open(m.root)
	if err != nil {
		return nil, ErrS3KeyUnavailable
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		return nil, ErrS3KeyUnavailable
	}
	winner, err := read()
	if err != nil {
		return nil, ErrS3KeyUnavailable
	}
	return winner, nil
}

func sealS3Credentials(key []byte, identity, access, secret string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrS3KeyUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrS3KeyUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, ErrS3KeyUnavailable
	}
	plain, err := json.Marshal([]string{access, secret})
	if err != nil {
		return nil, ErrS3KeyUnavailable
	}
	return aead.Seal(nonce, nonce, plain, []byte("derivatives-s3-v1:"+identity)), nil
}

func (m *Manager) s3Credentials(t *DerivativeS3Target) (string, string, error) {
	if t.ID == 0 && t.accessKey != "" && t.secretKey != "" {
		return t.accessKey, t.secretKey, nil
	}
	key, err := m.s3EncryptionKey(false)
	if err != nil {
		return "", "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", ErrS3KeyUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(t.Credentials) < aead.NonceSize()+aead.Overhead() {
		return "", "", ErrS3KeyUnavailable
	}
	plain, err := aead.Open(nil, t.Credentials[:aead.NonceSize()], t.Credentials[aead.NonceSize():], []byte("derivatives-s3-v1:"+t.IdentityHash))
	if err != nil {
		return "", "", ErrS3KeyUnavailable
	}
	var credentials []string
	if json.Unmarshal(plain, &credentials) != nil || len(credentials) != 2 || credentials[0] == "" || credentials[1] == "" {
		return "", "", ErrS3KeyUnavailable
	}
	return credentials[0], credentials[1], nil
}
