// This file is part of the go-meta library.
//
// Copyright (C) 2017 JAAK MUSIC LTD
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.
//
// If you have any questions please contact yo@jaak.io

// The migrate package provides a mechanism to perform schema migrations on
// SQLite3 databases.
//
// Typical usage would be:
//
//   var migrations = migrate.NewMigrations()
//
//   migrations.Add(1, `
//     CREATE TABLE artist (
//       object_id text NOT NULL,
//       name      text NOT NULL,
//       type      text NOT NULL,
//       mbid      text NOT NULL
//     );
//   `)
//
//   err := migrations.Run(db)
//
package migrate

import (
	"database/sql"
	"fmt"

	"github.com/mattes/migrate"
	"github.com/mattes/migrate/database/sqlite3"
	"github.com/mattes/migrate/source"
	"github.com/mattes/migrate/source/stub"
)

// Migrations is a set of SQLite3 database migrations.
type Migrations struct {
	*source.Migrations
}

// NewMigrations returns a new set of migrations.
func NewMigrations() *Migrations {
	return &Migrations{source.NewMigrations()}
}

// Add adds the given SQL to the set of migrations with the given version.
func (m *Migrations) Add(version uint, sql string) {
	ok := m.Migrations.Append(&source.Migration{
		Version:    version,
		Identifier: sql,
		Direction:  source.Up,
	})
	if !ok {
		panic(fmt.Sprintf("failed to add migration: %v", m))
	}
}

// Run runs the set of migrations on the given SQLite3 database.
func (m *Migrations) Run(db *sql.DB) error {
	dbDriver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
	if err != nil {
		return err
	}

	srcDriver, err := (&stub.Stub{}).Open("stub://")
	if err != nil {
		return err
	}
	srcDriver.(*stub.Stub).Migrations = m.Migrations

	migrations, err := migrate.NewWithInstance("stub", srcDriver, "sqlite3", dbDriver)
	if err != nil {
		return err
	}

	if err := migrations.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}

	return nil
}