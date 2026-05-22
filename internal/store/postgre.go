import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

type PostgresStore struct {
	db *sql.DB
}

type entry struct {
	id           string
	user_id      string
	api_key_hash string

	expiresAt time.Time
}