package data

import (
	"database/sql"
	"fmt"
)

var db *sql.DB

func main() {

	// This SQL statement uses the $1 parameter twice, and the value `123` will
	// be used in both locations where $1 appears.
	var stmt = "UPDATE foo SET bar = $1 + $2 WHERE bar = $1"
	var _, err = db.Exec(stmt, 123, 456)

	if err != nil {
		fmt.Println(err)
	}

	// Executing multiple statements
	// Having multiple statements in the same call is supported by the pq driver,
	// so long as the statements do not contain any placeholder parameters.
	stmt = `UPDATE foo SET bar = true; UPDATE foo SET baz = false;`
	_, err = db.Exec(stmt)
	if err != nil {
		fmt.Println(err)
	}
}
