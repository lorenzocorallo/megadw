package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/lorenzocorallo/megadw/internal/store"
)

// Service is the typed, atomic settings boundary. It keeps no mutable cache,
// so a second process instance cannot observe stale settings after restart.
type Service struct {
	DB  *store.DB
	Now func() time.Time
	mu  sync.Mutex
}

func NewService(db *store.DB) (*Service, error) {
	service := &Service{DB: db, Now: time.Now}
	if _, err := service.Get(context.Background()); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) Get(ctx context.Context) (Settings, error) {
	defaults := Default()
	values, err := s.DB.ReadSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	if len(values) == 0 {
		if err := s.Update(ctx, defaults); err != nil {
			return Settings{}, err
		}
		return defaults, nil
	}

	settings := defaults
	for key, raw := range values {
		var target any
		switch key {
		case "paths":
			target = &settings.Paths
		case "downloads":
			target = &settings.Downloads
		case "network":
			target = &settings.Network
		case "ui":
			target = &settings.UI
		default:
			continue
		}
		if err := json.Unmarshal(raw.JSON, target); err != nil {
			return Settings{}, fmt.Errorf("decode settings.%s: %w", key, err)
		}
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// Update validates the complete object before opening the write transaction.
// Therefore a rejected PUT cannot partially change one settings section.
func (s *Service) Update(ctx context.Context, value Settings) error {
	if err := value.Validate(); err != nil {
		return err
	}
	sections, err := marshalSections(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.DB.ReplaceSettings(ctx, sections, s.now()); err != nil {
		return err
	}
	return nil
}
