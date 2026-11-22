package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestSession(t *testing.T) {
	assert.Equal(t, dto.Session{ID: 5, CreatedAt: t0, LastUsedAt: t0.Add(time.Hour), ExpiresAt: t0.Add(24 * time.Hour)},
		Session(model.Session{ID: 5, UserID: 1, CreatedAt: t0.In(est), LastUsedAt: t0.Add(time.Hour),
			ExpiresAt: t0.Add(24 * time.Hour).In(est)}))
}

func TestSessionList(t *testing.T) {
	assert.Equal(t, dto.Items[dto.Session]{}, SessionList(nil))
	assert.Equal(t, dto.Items[dto.Session]{Items: []dto.Session{{ID: 2}, {ID: 1}}},
		SessionList([]model.Session{{ID: 2}, {ID: 1}}))
}
