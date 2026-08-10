package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/ferriskleier/delta/internal/apperror"
)

const (
	uiColorsKey       = "ui_colors"
	uiRatingCount     = 5
	uiHabitColorCount = 20
)

var uiColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// uiColors is the stored palette shape. It stays unexported because the seam
// deliberately exchanges raw JSON: the frontend owns the palette's meaning and
// the service only guarantees the shape.
type uiColors struct {
	Ratings map[string]string `json:"ratings"`
	Habits  []string          `json:"habits"`
}

// UIColors returns the stored custom pixel colors, or nil when the user has
// never saved a palette.
func (s *Service) UIColors(ctx context.Context) (json.RawMessage, error) {
	var value string
	err := s.Store.DB.QueryRowContext(ctx, "SELECT value FROM delta_metadata WHERE key = ?", uiColorsKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ui colors: %w", err)
	}
	return json.RawMessage(value), nil
}

// SetUIColors validates raw and stores it. The returned bytes are the stored
// canonical form, so callers do not have to read the row back.
func (s *Service) SetUIColors(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	stored, err := validateUIColors(raw)
	if err != nil {
		return nil, err
	}
	s.beforeWrite(ctx)
	if _, err := s.Store.DB.ExecContext(ctx,
		"INSERT OR REPLACE INTO delta_metadata(key, value) VALUES (?, ?)", uiColorsKey, string(stored)); err != nil {
		return nil, fmt.Errorf("save ui colors: %w", err)
	}
	return stored, nil
}

// ClearUIColors removes the stored palette, which returns the UI to its
// built-in colors.
func (s *Service) ClearUIColors(ctx context.Context) error {
	s.beforeWrite(ctx)
	if _, err := s.Store.DB.ExecContext(ctx, "DELETE FROM delta_metadata WHERE key = ?", uiColorsKey); err != nil {
		return fmt.Errorf("clear ui colors: %w", err)
	}
	return nil
}

func validateUIColors(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value uiColors
	if err := decoder.Decode(&value); err != nil {
		return nil, apperror.Wrap(apperror.CodeInvalidUIColors, "invalid colors JSON", err)
	}
	if decoder.More() {
		return nil, apperror.New(apperror.CodeInvalidUIColors, "colors JSON must be a single object")
	}
	if len(value.Ratings) != uiRatingCount {
		return nil, apperror.New(apperror.CodeInvalidUIColors, "colors ratings must have exactly the keys 1 through 5")
	}
	for rating := 1; rating <= uiRatingCount; rating++ {
		color, ok := value.Ratings[strconv.Itoa(rating)]
		if !ok {
			return nil, apperror.New(apperror.CodeInvalidUIColors, "colors ratings must have exactly the keys 1 through 5")
		}
		if !uiColorPattern.MatchString(color) {
			return nil, apperror.New(apperror.CodeInvalidUIColors, "colors must be hex values like #1a2b3c")
		}
	}
	if len(value.Habits) != uiHabitColorCount {
		return nil, apperror.New(apperror.CodeInvalidUIColors,
			fmt.Sprintf("colors habits must have exactly %d entries", uiHabitColorCount))
	}
	for _, color := range value.Habits {
		if !uiColorPattern.MatchString(color) {
			return nil, apperror.New(apperror.CodeInvalidUIColors, "colors must be hex values like #1a2b3c")
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode ui colors: %w", err)
	}
	return encoded, nil
}
