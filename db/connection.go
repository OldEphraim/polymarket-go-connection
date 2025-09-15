package db

import (
	"context"
	"database/sql"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
	_ "github.com/lib/pq"
)

type Store struct {
	*database.Queries
	db *sql.DB
}

func NewStore(connStr string) (*Store, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}

	return &Store{
		Queries: database.New(db),
		db:      db,
	}, nil
}

func (s *Store) ExecTx(ctx context.Context, fn func(*database.Queries) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	q := s.Queries.WithTx(tx)
	err = fn(q)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}
