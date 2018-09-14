// Copyright 2016 Documize Inc. <legal@documize.com>. All rights reserved.
//
// This software (Documize Community Edition) is licensed under
// GNU AGPL v3 http://www.gnu.org/licenses/agpl-3.0.en.html
//
// You can operate outside the AGPL restrictions by purchasing
// Documize Enterprise Edition and obtaining a commercial license
// by contacting <sales@documize.com>.
//
// https://documize.com

package database

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/documize/community/core/env"
	"github.com/jmoiron/sqlx"
)

// InstallUpgrade creates new database or upgrades existing database.
func InstallUpgrade(runtime *env.Runtime, existingDB bool) (err error) {
	amLeader := false

	// Get all SQL scripts.
	scripts, err := LoadScripts()
	if err != nil {
		runtime.Log.Error("Database: unable to load scripts", err)
		return
	}

	// Filter out database specific scripts.
	dbTypeScripts := SpecificScripts(runtime, scripts)
	if len(dbTypeScripts) == 0 {
		runtime.Log.Info(fmt.Sprintf("Database: unable to load scripts for database type %s", runtime.StoreProvider.Type()))
		return
	}

	runtime.Log.Info(fmt.Sprintf("Database: loaded  %d SQL scripts for provider %s", len(dbTypeScripts), runtime.StoreProvider.Type()))

	// Get current database version.
	currentVersion := 0
	if existingDB {
		currentVersion, err = CurrentVersion(runtime)
		if err != nil {
			runtime.Log.Error("Database: unable to get current version", err)
			return
		}

		runtime.Log.Info(fmt.Sprintf("Database: current version number is %d", currentVersion))
	}

	// Make a list of scripts to execute based upon current database state.
	toProcess := []Script{}
	for _, s := range dbTypeScripts {
		if s.Version > currentVersion || currentVersion == 0 {
			toProcess = append(toProcess, s)
		}
	}

	if existingDB {
		var err error
		amLeader, err = Lock(runtime, len(toProcess))
		if err != nil {
			runtime.Log.Error("Database: failed to lock existing database for processing", err)
		}
	} else {
		// New installation hopes that you are only spinning up one instance of Documize.
		// Assumption: nobody will perform the intial setup in a clustered environment.
		amLeader = true
	}

	tx, err := runtime.Db.Beginx()
	if err != nil {
		return Unlock(runtime, tx, err, amLeader)
	}

	// If currently running process is database leader then we perform upgrade.
	if amLeader {
		runtime.Log.Info(fmt.Sprintf("Database: %d SQL scripts to process", len(toProcess)))

		err = runScripts(runtime, tx, toProcess)
		if err != nil {
			runtime.Log.Error("Database: error processing SQL script", err)
		}

		return Unlock(runtime, tx, err, amLeader)
	}

	// If currently running process is a slave instance then we wait for migration to complete.
	targetVersion := toProcess[len(toProcess)-1].Version

	for targetVersion != currentVersion {
		time.Sleep(time.Second)
		runtime.Log.Info("Database: slave instance polling for upgrade process completion")
		tx.Rollback()

		// Get database version and check again.
		currentVersion, err = CurrentVersion(runtime)
		if err != nil {
			return Unlock(runtime, tx, err, amLeader)
		}
	}

	return Unlock(runtime, tx, nil, amLeader)
}

// Run SQL scripts to instal or upgrade this database.
func runScripts(runtime *env.Runtime, tx *sqlx.Tx, scripts []Script) (err error) {
	// We can have multiple scripts as each Documize database change has it's own SQL script.
	for _, script := range scripts {
		runtime.Log.Info(fmt.Sprintf("Databasse: processing SQL version %d", script.Version))

		err = executeSQL(tx, runtime.StoreProvider.Type(), script.Script)
		if err != nil {
			return err
		}

		// Record the fact we have processed this database script version.
		_, err = tx.Exec(runtime.StoreProvider.QueryRecordVersionUpgrade(script.Version))
		if err != nil {
			return err
		}
	}

	return nil
}

// executeSQL runs specified SQL commands.
func executeSQL(tx *sqlx.Tx, v env.StoreType, SQLfile []byte) error {
	// Turn SQL file contents into runnable SQL statements.
	stmts := getStatements(SQLfile)

	for _, stmt := range stmts {
		// MariaDB has no specific JSON column type (but has JSON queries)
		if v == env.StoreTypeMariaDB {
			stmt = strings.Replace(stmt, "` JSON", "` TEXT", -1)
		}

		_, err := tx.Exec(stmt)
		if err != nil {
			return err
		}
	}

	return nil
}

// getStatement strips out the comments and returns all the individual SQL commands (apart from "USE") as a []string.
func getStatements(bytes []byte) (stmts []string) {
	// Strip comments of the form '-- comment' or like this one /**/
	stripped := regexp.MustCompile("(?s)--.*?\n|/\\*.*?\\*/").ReplaceAll(bytes, []byte("\n"))

	// Break into lines using ; terminator.
	lines := strings.Split(string(stripped), ";")

	// Prepare return data.
	stmts = make([]string, 0, len(lines))

	for _, v := range lines {
		trimmed := strings.TrimSpace(v)
		// Process non-empty lines and exclude "USE dbname" command
		if len(trimmed) > 0 && !strings.HasPrefix(strings.ToUpper(trimmed), "USE ") {
			stmts = append(stmts, trimmed+";")
		}
	}

	return
}

// CurrentVersion returns number that represents the current database version number.
// For example 23 represents the 23rd iteration of the database.
func CurrentVersion(runtime *env.Runtime) (version int, err error) {
	row := runtime.Db.QueryRow(runtime.StoreProvider.QueryGetDatabaseVersion())

	var currentVersion string
	err = row.Scan(&currentVersion)
	if err != nil {
		currentVersion = "0"
	}

	return extractVersionNumber(currentVersion), nil
}

// Turns legacy "db_00021.sql" and new "21" format into version number 21.
func extractVersionNumber(s string) int {
	// Good practice in case of human tampering.
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)

	// Remove any quotes from JSON string.
	s = strings.Replace(s, "\"", "", -1)

	// Remove legacy version string formatting.
	// We know just store the number.
	s = strings.Replace(s, "db_000", "", 1)
	s = strings.Replace(s, ".sql", "", 1)

	i, err := strconv.Atoi(s)
	if err != nil {
		i = 0
	}

	return i
}
