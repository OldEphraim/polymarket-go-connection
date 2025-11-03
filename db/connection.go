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

func (s *Store) ExecTx(ctx context.Context, fn func(*database.Queries) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	q := s.Queries.WithTx(tx)
	if err = fn(q); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DB() *sql.DB { return s.db }
