package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/lib/pq"
	"github.com/saalikmubeen/greenlight/internal/validator"
)

// Movie type whose fields describe the movie.
// Note that the Runtime type uses a custom Runtime type instead of int32. Furthermore, the omitempty
// directive on the Runtime type will still work on this: if the Runtime field has the underlying
// value 0, then it will be considered empty and omitted -- and the MarshalJSON() method won't
// be called.
type Movie struct {
	ID        int64     `json:"id"` // Unique integer ID for the movie
	CreatedAt time.Time `json:"-"`  // Use the - directive to never export in JSON output
	Title     string    `json:"title"`
	Year      int32     `json:"year,omitempty"` // Movie release year0
	Runtime   Runtime   `json:"runtime,omitempty"`
	Genres    []string  `json:"genres,omitempty"`
	Version   int32     `json:"version"` // The version number starts at 1 and is incremented each
	// time the movie information is updated.
}

// MovieModel struct wraps a sql.DB connection pool and allows us to work with Movie struct type
// and the movies table in our database.
type MovieModel struct {
	DB       *sql.DB
	InfoLog  *log.Logger
	ErrorLog *log.Logger
}

// Insert accepts a pointer to a movie struct, which should contain the data for the
// new record and inserts the record into the movies table.
func (m MovieModel) Insert(movie *Movie) error {
	query := `
		INSERT INTO movies (title, year, runtime, genres) 
		VALUES ($1, $2, $3, $4) 
		RETURNING id, created_at, version
		`

	// we have a RETURNING clause. This is a PostgreSQL-specific clause
	// (it’s not part of the SQL standard) that you can use to return values from any record
	// that is being manipulated by an INSERT, UPDATE or DELETE statement

	// Create a context with a 3-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Create an args slice containing the values for the placeholder parameters from the movie
	// struct. Declaring this slice immediately next to our SQL query helps to make it nice and
	// clear *what values are being user where* in the query

	// You can also use the pq.Array() adapter function in the same way with []bool, []byte,
	//  []int32, []int64, []float32 and []float64 slices in your Go code.
	args := []interface{}{movie.Title, movie.Year, movie.Runtime, pq.Array(movie.Genres)}

	return m.DB.QueryRowContext(ctx, query, args...).Scan(&movie.ID, &movie.CreatedAt, &movie.Version)
}

// Get fetches a record from the movies table and returns the corresponding Movie struct.
// It cancels the query call if the SQL query does not finish within 3 seconds.
func (m MovieModel) Get(id int64) (*Movie, error) {
	// The PostgreSQL bigserial type that we're using for the movie ID starts auto-incrementing
	// at 1 by default, so we know that no movies will have ID values less tan that.
	// To avoid making an unnecessary database call,
	// we take a shortcut and return an ErrRecordNotFound error straight away.
	if id < 1 {
		return nil, ErrRecordNotFound
	}

	// query := `
	// 	SELECT pg_sleep(10) id, created_at, title, year, runtime, genres, version
	//     FROM movies
	// 	WHERE id = $1
	// 	`

	query := `
		SELECT id, created_at, title, year, runtime, genres, version
        FROM movies
 		WHERE id = $1
 		`

	var movie Movie

	// Use the context.WithTimeout() function to create a context.Context which carries a 3-second
	// timeout deadline. Note, that we're using the empty context.Background() as the
	// 'parent' context.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	//
	// ** Defer cancel() **
	// Defer cancel to make sure that we cancel the context before the Get() method returns
	// The defer cancel() line is necessary because it ensures that the resources associated
	// with our context will always be released before the Get() method returns,
	// thereby preventing a memory leak. Without it, the resources won’t be released
	// until either the 3- second timeout is hit or the parent context
	// (which in this specific example is context.Background()) is canceled.

	/*More precisely, our context (the one with the 3-second timeout) has a Done
	channel, and when the timeout is reached the Done channel will be closed.
	While the SQL query is running, our database driver pq is also running a
	background goroutine which listens on this Done channel. If the channel
	gets closed, then pq sends a cancellation signal to PostgreSQL. PostgreSQL
	terminates the query, and then sends the error message that we see above as a
	response to the original pq goroutine. That error message is then returned to our
	database model’s Get() method. */
	defer cancel()

	// Use the QueryRowContext() method to execute the query, passing in the context with the
	// deadline ctx as the first argument.
	err := m.DB.QueryRowContext(ctx, query, id).Scan(
		&movie.ID,
		&movie.CreatedAt,
		&movie.Title,
		&movie.Year,
		&movie.Runtime,
		pq.Array(&movie.Genres),
		&movie.Version)

	// Handle any errors. If there was no matching movie found, Scan() will return a sql.ErrNoRows
	// error. We check for this and return our custom ErrRecordNotFound error instead.
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}

	return &movie, nil
}

// Update updates a specific movie in the movies table.
func (m MovieModel) Update(movie *Movie) error {

	// ** Optimistic Concurrency Control
	// The update is only executed if the version number in the database is still
	// the same as the version number that was passed in with the movie struct
	// i.e the version of the movie user has is the same as the version in the database
	// If the version number has changed in database, we know that another user has updated
	// the movie record since the user last fetched it. In that case, we return an ErrEditConflict
	// error to indicate that the update cannot be performed.
	// version = version = uuid_generate_v4() // version is a UUID
	query := `
		UPDATE movies
		SET title = $1, year = $2, runtime = $3, genres = $4, version = version + 1
		WHERE id = $5 AND version = $6 
		RETURNING version
		`

	// Create an args slice containing the values for the placeholder parameters.
	args := []interface{}{
		movie.Title,
		movie.Year,
		movie.Runtime,
		pq.Array(movie.Genres),
		movie.ID,
		movie.Version, // Add the expected movie version.
	}

	// Create a context with a 3-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Execute the SQL query. If no matching row could be found, we know the movie version
	// has changed (or the record has been deleted) and we return ErrEditConflict.
	err := m.DB.QueryRowContext(ctx, query, args...).Scan(&movie.Version)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrEditConflict
		default:
			return err
		}
	}

	return nil
}

// Delete is a placeholder method for deleting a specific record in the movies table.
func (m MovieModel) Delete(id int64) error {
	// Return an ErrRecordNotFound error if the movie ID is less than 1
	if id < 1 {
		return ErrRecordNotFound
	}

	query := `
		DELETE FROM movies
		WHERE id = $1
		`

	// Create a context with a 3-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Execute the SQL query using the Exec() method,
	// passing in the id variable as the value for the placeholder parameter. The Exec(
	// ) method returns a sql.Result object.
	result, err := m.DB.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}

	// Call the RowsAffected() method on the sql.Result
	// object to get the number of rows affected by the query.
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	// If no rows were affected,
	// we know that the movies table didn't contain a record with the provided ID at the moment
	// we tried to delete it. In that case we return an ErrRecordNotFound error.
	if rowsAffected == 0 {
		return ErrRecordNotFound
	}

	return nil
}

// GetAll returns a list of movies in the form of a string of Movie type
// based on a set of provided filters.
func (m MovieModel) GetAll(title string, genres []string, filters Filters) ([]*Movie, Metadata, error) {
	// This SQL query is designed so that each of the filters behaves like it is ‘optional’.
	// Add an ORDER BY clause and interpolate the sort column and direction using fmt.Sprintf.
	// Importantly, notice that we also include a secondary sort on the movie ID to ensure
	// a consistent ordering. Furthermore, we include LIMIT and OFFSET clauses with placeholder
	// parameter values for pagination implementation. The window function is used to calculate
	// the total filtered rows which will be used in our pagination metadata.
	// Complete list of postgres array functions and operators:
	// https://www.postgresql.org/docs/9.6/functions-array.html
	query := fmt.Sprintf(`
		SELECT count(*) OVER(), id, created_at, title, year, runtime, genres, version
		FROM movies
		WHERE (to_tsvector('simple', title) @@ plainto_tsquery('simple', $1) OR $1 = '')
		AND (genres @> $2 OR $2 = '{}')
		ORDER BY %s %s, id ASC
		LIMIT $3 OFFSET $4`,
		filters.sortColumn(), filters.sortDirection())

	// Create a context with a 3-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Organize our four placeholder parameter values in a slice.
	args := []interface{}{title, pq.Array(genres), filters.limit(), filters.offset()}

	// Use QueryContext to execute the query. This returns a sql.Rows result set containing
	// the result.
	rows, err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, Metadata{}, err
	}

	// Importantly, defer a call to rows.Close() to ensure that the result set is closed
	// before GetAll returns.
	defer func() {
		if err := rows.Close(); err != nil {
			m.ErrorLog.Println(err)
		}
	}()

	// Declare a totalRecords variable
	totalRecords := 0

	// Initialize an empty slice to hold the movie data.
	movies := []*Movie{}

	// Use rows.Next to iterate through the rows in the result set.
	for rows.Next() {
		// Initialize an empty Movie struct to hold the data for an individual movie.
		var movie Movie

		// Scan the values from the row into the Movie struct. Again, note that we're using
		// the pq.Array adapter on the genres field.
		err := rows.Scan(
			&totalRecords, // Scan the count from the window function into totalRecords.
			&movie.ID,
			&movie.CreatedAt,
			&movie.Title,
			&movie.Year,
			&movie.Runtime,
			pq.Array(&movie.Genres),
			&movie.Version,
		)
		if err != nil {
			return nil, Metadata{}, err
		}

		// Add the Movie struct to the slice
		movies = append(movies, &movie)
	}

	// When the rows.Next() loop has finished, call rows.Err() to retrieve any error
	// that was encountered during the iteration.
	if err = rows.Err(); err != nil {
		return nil, Metadata{}, err
	}

	// Generate a Metadata struct, passing in the total record count and pagination parameters
	// from the client.
	metadata := calculateMetadata(totalRecords, filters.Page, filters.PageSize)

	// If everything went OK, then return the slice of the movies and metadata.
	return movies, metadata, nil
}

// ValidateMovie runs validation checks on the Movie type.
func ValidateMovie(v *validator.Validator, movie *Movie) {
	// Check movie.Title
	v.Check(movie.Title != "", "title", "must be provided")
	v.Check(len(movie.Title) <= 500, "title", "must not be more than 500 bytes long")

	// Check movie.Year
	v.Check(movie.Year != 0, "year", "must be provided")
	v.Check(movie.Year >= 1888, "year", "must be greater than 1888")
	v.Check(movie.Year <= int32(time.Now().Year()), "year", "must not be in the future")

	// Check movie.Runtime
	v.Check(movie.Runtime != 0, "runtime", "must be provided")
	v.Check(movie.Runtime > 0, "runtime", "must be a positive integer")

	// Check movie.Genres
	v.Check(movie.Genres != nil, "genres", "must be provided")
	v.Check(len(movie.Genres) >= 1, "genres", "must contain at least 1 genre")
	v.Check(len(movie.Genres) <= 5, "genres", "must not contain more than 5 genres")
	v.Check(validator.Unique(movie.Genres), "genres", "must not contain duplicate values")

}
