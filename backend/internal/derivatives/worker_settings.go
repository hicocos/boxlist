package derivatives

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Independent of environment resource budgets. Limit edits affect future claims,
// never cancel work already admitted. Persist before publishing the new limit.
type DerivativeWorkerSetting struct {
	ID        uint `gorm:"primaryKey;autoIncrement:false"`
	Workers   int  `gorm:"not null;default:1"`
	UpdatedAt time.Time
}
type WorkerConfiguration struct {
	Workers       int `json:"workers"`
	ActiveWorkers int `json:"active_workers"`
	MinWorkers    int `json:"min_workers"`
	MaxWorkers    int `json:"max_workers"`
}

var ErrWorkerLimit = errors.New("invalid_derivative_worker_limit")

func (m *Manager) initWorkerSettings() error {
	if e := m.db.AutoMigrate(&DerivativeWorkerSetting{}); e != nil {
		return e
	}
	var setting DerivativeWorkerSetting
	e := m.db.First(&setting, 1).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		setting = DerivativeWorkerSetting{ID: 1, Workers: 1}
		e = m.db.Create(&setting).Error
	}
	if e != nil {
		return e
	}
	if setting.Workers < 1 || setting.Workers > 4 {
		return ErrWorkerLimit
	}
	m.workerLimit = setting.Workers
	return nil
}
func (m *Manager) workerConfigurationLocked() WorkerConfiguration {
	return WorkerConfiguration{Workers: m.workerLimit, ActiveWorkers: len(m.activeClaims), MinWorkers: 1, MaxWorkers: 4}
}
func (m *Manager) WorkerConfiguration() WorkerConfiguration {
	m.gate.Lock()
	defer m.gate.Unlock()
	return m.workerConfigurationLocked()
}
func (m *Manager) SaveWorkerConfiguration(ctx context.Context, workers int) (WorkerConfiguration, error) {
	if workers < 1 || workers > 4 {
		return WorkerConfiguration{}, ErrWorkerLimit
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	if m.ctx.Err() != nil {
		return WorkerConfiguration{}, ErrClosed
	}
	e := m.transaction(ctx, func(tx *gorm.DB) error {
		setting := DerivativeWorkerSetting{ID: 1, Workers: workers, UpdatedAt: time.Now()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"workers", "updated_at"})}).Create(&setting).Error
	})
	if e != nil {
		return WorkerConfiguration{}, e
	}
	m.workerLimit = workers
	m.signal()
	return m.workerConfigurationLocked(), nil
}
