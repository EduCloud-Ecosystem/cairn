// SPDX-License-Identifier: AGPL-3.0-or-later
package sqlite

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

// OpenReadOnly opens an existing schema without migrations, creation or writes.
func OpenReadOnly(path string) (*Store, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("regular existing database required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	// All getters observe one read snapshot on the sole connection. Close ends it.
	if _, err = db.Exec("BEGIN"); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
