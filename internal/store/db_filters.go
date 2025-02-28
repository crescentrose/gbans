package store

import (
	"context"
	"github.com/leighmacdonald/gbans/internal/model"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"regexp"
)

func (db *pgStore) SaveFilter(ctx context.Context, filter *model.Filter) error {
	_, err := db.insertFilter(ctx, filter)
	return err
}

func (db *pgStore) insertFilter(ctx context.Context, filter *model.Filter) (*model.Filter, error) {
	const q = `INSERT INTO filtered_word (word, created_on) VALUES ($1, $2) RETURNING word_id`
	if err := db.c.QueryRow(ctx, q, filter.Pattern.String(), filter.CreatedOn).Scan(&filter.WordID); err != nil {
		return nil, dbErr(err)
	}
	log.Debugf("Created filter: %d", filter.WordID)
	return filter, nil
}

func (db *pgStore) DropFilter(ctx context.Context, filter *model.Filter) error {
	const q = `DELETE FROM filtered_word WHERE word_id = $1`
	if _, err := db.c.Exec(ctx, q, filter.WordID); err != nil {
		return dbErr(err)
	}
	log.Debugf("Deleted filter: %d", filter.WordID)
	return nil
}

func (db *pgStore) GetFilterByID(ctx context.Context, wordId int, f *model.Filter) error {
	const q = `SELECT word_id, word, created_on from filtered_word WHERE word_id = $1`
	var w string
	if err := db.c.QueryRow(ctx, q, wordId).Scan(&f.WordID, &w, &f.CreatedOn); err != nil {
		return errors.Wrapf(err, "Failed to load filter")
	}
	rx, er := regexp.Compile(w)
	if er != nil {
		return er
	}
	f.Pattern = rx
	return nil
}

func (db *pgStore) GetFilters(ctx context.Context) ([]model.Filter, error) {
	rows, err := db.c.Query(ctx, `SELECT word_id, word, created_on from filtered_word`)
	if err != nil {
		return nil, dbErr(err)
	}
	var filters []model.Filter
	defer rows.Close()
	for rows.Next() {
		var f model.Filter
		var w string
		if err = rows.Scan(&f.WordID, &w, &f.CreatedOn); err != nil {
			return nil, errors.Wrapf(err, "Failed to load filter")
		}
		rx, er := regexp.Compile(w)
		if er != nil {
			return nil, er
		}
		f.Pattern = rx
		filters = append(filters, f)
	}
	return filters, nil
}
