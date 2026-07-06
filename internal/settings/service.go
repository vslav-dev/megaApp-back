package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const defaultMetricsSettings = "{}"

var ErrInvalidSetting = errors.New("invalid setting name")
var ErrInvalidSettingPayload = errors.New("request body must contain exactly one setting")

type UpdateField string

const (
	UpdateFieldDarkTheme            UpdateField = "darkTheme"
	UpdateFieldSelectedChapterFood  UpdateField = "selectedChapterFood"
	UpdateFieldSelectedChapterMoney UpdateField = "selectedChapterMoney"
	UpdateFieldSelectedChapterTime  UpdateField = "selectedChapterTime"
	UpdateFieldLiteVersion          UpdateField = "liteVersion"
	UpdateFieldHeight               UpdateField = "height"
)

type UpdateInput struct {
	Field       UpdateField
	BoolValue   *bool
	HeightValue *int64
}

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Get(ctx context.Context, userID int64, fallbackUserName string) (UserSettings, error) {
	stored, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return UserSettings{}, err
	}
	if stored == nil {
		stored = defaultStoredSettings()
		if err := s.repo.Upsert(ctx, userID, *stored); err != nil {
			return UserSettings{}, err
		}
	}

	isAdmin, userName, err := s.repo.GetUserAdminAndName(ctx, userID)
	if err != nil {
		return UserSettings{}, err
	}
	if userName == "" {
		userName = fallbackUserName
	}

	return UserSettings{
		SelectedChapterFood:  stored.SelectedChapterFood,
		SelectedChapterMoney: stored.SelectedChapterMoney,
		SelectedChapterTime:  stored.SelectedChapterTime,
		DarkTheme:            stored.DarkTheme,
		LiteVersion:          stored.LiteVersion,
		Height:               stored.Height,
		UserName:             userName,
		IsUserAdmin:          isAdmin,
	}, nil
}

func (s *Service) Put(ctx context.Context, userID int64, input UpdateInput) error {
	stored, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	if stored == nil {
		stored = defaultStoredSettings()
	}

	switch input.Field {
	case UpdateFieldDarkTheme:
		if input.BoolValue == nil {
			return fmt.Errorf("invalid darkTheme value")
		}
		stored.DarkTheme = *input.BoolValue
	case UpdateFieldSelectedChapterFood:
		if input.BoolValue == nil {
			return fmt.Errorf("invalid selectedChapterFood value")
		}
		stored.SelectedChapterFood = *input.BoolValue
	case UpdateFieldSelectedChapterMoney:
		if input.BoolValue == nil {
			return fmt.Errorf("invalid selectedChapterMoney value")
		}
		stored.SelectedChapterMoney = *input.BoolValue
	case UpdateFieldSelectedChapterTime:
		if input.BoolValue == nil {
			return fmt.Errorf("invalid selectedChapterTime value")
		}
		stored.SelectedChapterTime = *input.BoolValue
	case UpdateFieldLiteVersion:
		if input.BoolValue == nil {
			return fmt.Errorf("invalid liteVersion value")
		}
		stored.LiteVersion = *input.BoolValue
	case UpdateFieldHeight:
		stored.Height = input.HeightValue
	default:
		return ErrInvalidSetting
	}

	return s.repo.Upsert(ctx, userID, *stored)
}

func (s *Service) Post(ctx context.Context, userID int64, request UserSettings) error {
	return s.repo.Upsert(ctx, userID, StoredSettings{
		SelectedChapterFood:  request.SelectedChapterFood,
		SelectedChapterMoney: request.SelectedChapterMoney,
		SelectedChapterTime:  request.SelectedChapterTime,
		DarkTheme:            request.DarkTheme,
		LiteVersion:          request.LiteVersion,
		Height:               request.Height,
	})
}

func (s *Service) GetMetricsSettings(ctx context.Context, userID int64) (json.RawMessage, error) {
	stored, err := s.repo.GetMetricsSettingsByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if stored == "" {
		return json.RawMessage(defaultMetricsSettings), nil
	}

	return json.RawMessage(stored), nil
}

func (s *Service) PutMetricsSettings(ctx context.Context, userID int64, value json.RawMessage) error {
	return s.repo.UpsertMetricsSettings(ctx, userID, string(value))
}

func defaultStoredSettings() *StoredSettings {
	return &StoredSettings{}
}

func ParseUpdateInput(payload map[string]any) (UpdateInput, error) {
	if len(payload) != 1 {
		return UpdateInput{}, ErrInvalidSettingPayload
	}

	for key, value := range payload {
		switch key {
		case string(UpdateFieldDarkTheme), string(UpdateFieldSelectedChapterFood), string(UpdateFieldSelectedChapterMoney), string(UpdateFieldSelectedChapterTime), string(UpdateFieldLiteVersion):
			parsed, ok := value.(bool)
			if !ok {
				return UpdateInput{}, fmt.Errorf("invalid %s value", key)
			}
			return UpdateInput{Field: UpdateField(key), BoolValue: &parsed}, nil
		case string(UpdateFieldHeight):
			parsed, ok := parseHeight(value)
			if !ok {
				return UpdateInput{}, fmt.Errorf("invalid height value")
			}
			return UpdateInput{Field: UpdateFieldHeight, HeightValue: parsed}, nil
		default:
			return UpdateInput{}, ErrInvalidSetting
		}
	}

	return UpdateInput{}, ErrInvalidSettingPayload
}

func parseHeight(value any) (*int64, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, true
	case float64:
		parsed := int64(typed)
		if float64(parsed) != typed {
			return nil, false
		}
		return &parsed, true
	case int:
		parsed := int64(typed)
		return &parsed, true
	case int64:
		parsed := typed
		return &parsed, true
	}

	return nil, false
}
