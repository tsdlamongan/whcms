package httpx

import (
	"strconv"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/gofiber/fiber/v3"
)

// DateLayout is the wire format of date-only query filters (?date_from=...).
const DateLayout = "2006-01-02"

// QueryID reads an optional positive-integer filter such as ?client_id=12. It
// returns 0 when the parameter is absent or blank (= "no filter") and a
// VALIDATION error when it is present but not a positive integer, so a typo
// can never silently widen a list to every row.
func QueryID(c fiber.Ctx, name string) (int64, error) {
	raw := c.Query(name)
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid "+name,
			apperr.FieldError{Field: name, Message: "must be a positive integer"})
	}
	return id, nil
}

// QueryIDs reads an optional comma-separated list of positive integers such as
// ?ids=3,4,5 (blanks around items are tolerated). It returns nil when the
// parameter is absent or blank and a VALIDATION error when any item is not a
// positive integer or the list is longer than MaxPerPage - one page's worth of
// references, which is all a list screen ever needs to resolve.
func QueryIDs(c fiber.Ctx, name string) ([]int64, error) {
	raw := c.Query(name)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > MaxPerPage {
		return nil, apperr.Validation("too many "+name,
			apperr.FieldError{Field: name, Message: "at most " + strconv.Itoa(MaxPerPage) + " ids"})
	}
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || id < 1 {
			return nil, apperr.Validation("invalid "+name,
				apperr.FieldError{Field: name, Message: "must be comma-separated positive integers"})
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// QueryDate reads an optional YYYY-MM-DD filter such as ?date_from=2026-01-31.
// It returns the zero time when the parameter is absent or blank and a
// VALIDATION error when it is present but malformed.
func QueryDate(c fiber.Ctx, name string) (time.Time, error) {
	raw := c.Query(name)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(DateLayout, raw)
	if err != nil {
		return time.Time{}, apperr.Validation("invalid "+name,
			apperr.FieldError{Field: name, Message: "must be YYYY-MM-DD"})
	}
	return t, nil
}
