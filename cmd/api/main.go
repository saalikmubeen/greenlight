package main

import (
	"context"
	"database/sql"
	"expvar"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/saalikmubeen/greenlight/internal/data"
	"github.com/saalikmubeen/greenlight/internal/jsonlog"
	"github.com/saalikmubeen/greenlight/internal/mailer"
	"github.com/saalikmubeen/greenlight/internal/vcs"

	// Import the pq driver so that it can register itself with the database/sql
	// package. Note that we alias this import to the blank identifier, to stop the Go
	// compiler complaining that the package isn't being used.
	_ "github.com/lib/pq"
	//  The golang-migrate/migrate Go package to automatically execute your
	//  database migrations on application start up.
	// "github.com/golang-migrate/migrate/v4"
	// "github.com/golang-migrate/migrate/v4/database/postgres"
	// _ "github.com/golang-migrate/migrate/v4/source/file"
)

// Set version of application corresponding to value of vcs.Version.
var (
	version = vcs.Version()
	// Create a buildTime variable to hold the executable binary build time.
	// Note that this must be a string type, as the -X linker flag will only work
	// with string variables.
	buildTime string
	// buildTime is set and injected at compile or build time using
	// -ldflags and the -X linker flag which allows you to assign or ‘burn-in’ a
	// value to a variable when running go build like this:
	// current_time = $(shell date --iso-8601=seconds)
	// go build -ldflags='-s -X main.buildTime=${current_time}' -o=./bin/api ./cmd/api
	// The -X linker flag allows you to set the value of a string variable at compile time.

	// To test this, you can run the following command in the terminal:
	// ./bin/api -version
)

// Define a config struct.
type config struct {
	port int
	env  string
	// db struct field holds the configuration settings for our database connection pool.
	// For now this only holds the DSN, which we read in from a command-line flag.
	db struct {
		dsn string

		/* You should explicitly set a MaxOpenConns value. This should be comfortably below any hard limits
		on the number of connections imposed by your database and infrastructure.
		By default, the number of open connections is unlimited.
		By default PostgreSQL has a hard limit of 100 open connections.
		In general, higher MaxOpenConns and MaxIdleConns values will lead to better performance.
		Be aware that having a too-large idle connection pool (with connections that are not frequently re-used)
		can actually lead to reduced performance and unnecessary resource consumption. */
		maxOpenConns int
		/* Number of idle connections in the pool. By default, the maximum number of idle connections is 2.
		In theory, allowing a higher number of idle connections in the pool will improve
		performance because it makes it less likely that a new connection needs to be established from scratch
		— therefore helping to save resources. */
		maxIdleConns int
		/* This works in a very similar way to ConnMaxLifetime, except it sets the maximum length of time that a
		connection can be idle for before it is marked as expired. By default there’s no limit.
		If we set ConnMaxIdleTime to 1 hour, for example, any connections that have sat idle in the pool for
		1 hour since last being used will be marked as expired and removed by the background cleanup operation.
		You should generally set a ConnMaxIdleTime value to remove idle connections that haven’t been used for a long time */
		maxIdleTime string

		/* The maximum length of time that a connection can be reused for. By default, there’s no maximum
		lifetime and connections will be reused forever.
		If we set ConnMaxLifetime to one hour, for example, it means that all connections will be marked as ‘expired’
		one hour after they were first created, and cannot be reused after they’ve expired.
		It’s probably OK to leave ConnMaxLifetime as unlimited, unless your database imposes a
		hard limit on connection lifetime. */
		// ConnMaxLifeTime
	}
	// Add a new limiter struct containing fields for the request-per-second and burst
	// values, and a boolean field which we can use to enable/disable rate limiting.
	limiter struct {
		rps     float64 // requests per second
		burst   int     // burst or bucket size
		enabled bool
	}
	smtp struct {
		host     string
		port     int
		username string
		password string
		sender   string
	}
	cors struct {
		trustedOrigins []string
	}
}

// Define an application struct to hold dependencies for our HTTP handlers, helpers, and
// middleware.
type application struct {
	config config
	logger *jsonlog.Logger
	models data.Models
	mailer mailer.Mailer
	wg     sync.WaitGroup
}

func main() {
	// Declare an instance of the config struct.
	var cfg config

	// Read the value of the port and env command-line flags into the config struct.
	// We default to using the port number 4000 and the environment "development" if no
	// corresponding flags are provided.
	flag.IntVar(&cfg.port, "port", 4000, "API server port")
	flag.StringVar(&cfg.env, "env", "development", "Environment (development|staging|production")

	// Read the DSN Value from the db-dsn command-line flag into the config struct.
	// We default to using our development DSN if no flag is provided.
	pw := os.Getenv("DB_PW")
	flag.StringVar(&cfg.db.dsn, "db-dsn",
		fmt.Sprintf("postgres://greenlight:%s@localhost/greenlight?sslmode=disable",
			pw), "PostgreSQL DSN")

	// Read the connection pool settings from command-line flags into the config struct.
	// Notice the default values that we're using?
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", 25,
		"PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", 25,
		"PostgreSQL max open idle connections")
	flag.StringVar(&cfg.db.maxIdleTime, "db-max-idle-time", "15m",
		"PostgreSQL max connection idle time")

	// Read the limiter settings from the command-line flags into the config struct.
	// We use true as the default for 'enabled' setting.
	flag.Float64Var(&cfg.limiter.rps, "limiter-rps", 2, "Rate limiter maximum requests per second")
	flag.IntVar(&cfg.limiter.burst, "limiter-burst", 4, "Rate limiter maximum burst")
	flag.BoolVar(&cfg.limiter.enabled, "limiter-enabled", true, "Enable rate limiter")

	// Read the SMTP server configuration settings into the config struct, using the
	// Mailtrap settings as the default values.
	mtUser := os.Getenv("MAILTRAP_USER")
	mtPw := os.Getenv("MAILTRAP_PW")
	flag.StringVar(&cfg.smtp.host, "smtp-host", "smtp.mailtrap.io", "SMTP host")
	flag.IntVar(&cfg.smtp.port, "smtp-port", 2525, "SMTP port")
	flag.StringVar(&cfg.smtp.username, "smtp-username", mtUser, "SMTP username")
	flag.StringVar(&cfg.smtp.password, "smtp-password", mtPw, "SMTP password")
	flag.StringVar(&cfg.smtp.sender, "smtp-sender", "DoNotReply <3fc3f54366-09689f+1@inbox.mailtrap.io>", "SMTP sender")

	// Use flag.Func function to process the -cors-trusted-origins command line flag. In this we
	// use the strings.Field function to split the flag value into slice based on whitespace
	// characters and assign it to our config struct. Importantly, if the -cors-trusted-origins
	// flag is not present, contains the empty string, or contains only whitespace, then
	// strings.Fields will return an empty []string slice.
	// cors-trusted-origins will be a string containing a space-separated list of trusted origins.
	// For example, "http://localhost:4000 http://localhost:4001 http://localhost:4002"
	flag.Func("cors-trusted-origins", "Trusted CORS origins (space separated)", func(val string) error {
		cfg.cors.trustedOrigins = strings.Fields(val)
		return nil
	})

	// Create a new version boolean flag with the default value of false.
	displayVersion := flag.Bool("version", false, "Display version and exit")

	flag.Parse()

	// If the version flag value is true, then print out the version number and immediately exit.
	if *displayVersion {
		fmt.Printf("Version:\t%s\n", version)

		// Print out the contents of the buildTime variable.
		fmt.Printf("Build time:\t%s\n", buildTime)
		os.Exit(0)
	}

	// Initialize a new jsonlog.Logger which writes any messages *at or above* the INFO
	// severity level to the standard out stream.
	logger := jsonlog.NewLogger(os.Stdout, jsonlog.LevelInfo)

	// Call the openDB() helper function (see below) to create teh connection pool,
	// passing in the config struct. If this returns an error,
	// we log it and exit the application immediately.
	db, err := openDB(cfg)
	if err != nil {
		logger.PrintFatal(err, nil)
	}

	// To automatically execute your database migrations on application start up
	// golang-migrate/migrate
	// migrationDriver, err := postgres.WithInstance(db, &postgres.Config{})
	// if err != nil {
	// 	logger.PrintFatal(err, nil)
	// }
	// migrator, err := migrate.NewWithDatabaseInstance("../../migrations", "postgres", migrationDriver)
	// if err != nil {
	// 	logger.PrintFatal(err, nil)
	// }
	// err = migrator.Up()
	// if err != nil && err != migrate.ErrNoChange {
	// 	logger.PrintFatal(err, nil)
	// }
	// fmt.Printf("database migrations applied")

	// Defer a call to db.Close() so that the connection pool is closed before the main()
	// function exits.
	defer func() {
		if err := db.Close(); err != nil {
			logger.PrintFatal(err, nil)
		}
	}()

	logger.PrintInfo("database connection pool established", nil)

	// Publish a new "version" varaible in the expar var handler
	// containing our application version number.
	// The first part of this — expvar.NewString("version") — creates a new
	// expvar.String type, then publishes it so it appears in the expvar handler’s
	// JSON response with the name "version", and then returns a pointer to it.
	// Then we use the Set() method on it to assign an actual value to the pointer.
	// Go also provides functions for a few other common data types:
	// NewFloat(), NewInt() and NewMap()
	expvar.NewString("version").Set(version)

	// Publish the number of activate goroutines.
	expvar.Publish("goroutines", expvar.Func(func() interface{} {
		return runtime.NumGoroutine()
	}))

	// Publish the database connection pool statistics.
	// (such as the number of idle and in-use connections)
	expvar.Publish("database", expvar.Func(func() interface{} {
		return db.Stats()
	}))

	// Publish the current Unix timestamp.
	expvar.Publish("timestamp", expvar.Func(func() interface{} {
		return time.Now().Unix()
	}))

	// Declare an instance of the application struct, containing the config struct and the infoLog.
	app := &application{
		config: cfg,
		logger: logger,
		models: data.NewModels(db),
		mailer: mailer.New(cfg.smtp.host, cfg.smtp.port, cfg.smtp.username,
			cfg.smtp.password, cfg.smtp.sender),
	}

	// Call app.server() to start the server.
	if err := app.serve(); err != nil {
		logger.PrintFatal(err, nil)
	}
}

// openDB returns a sql.DB connection pool to postgres database
func openDB(cfg config) (*sql.DB, error) {
	// Use sql.Open() to create an empty connection pool, using the DSN from the config struct.
	db, err := sql.Open("postgres", cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	// Set the maximum number of open (in-use + idle) connections in the pool.
	// Note that passing a value less than or equal to 0 will mean there is no limit.
	db.SetMaxOpenConns(cfg.db.maxOpenConns)

	// Set the maximum number of idle connection in the pool. Again,
	// passing a value less than or equal to 0 will mean there is no limit
	db.SetMaxIdleConns(cfg.db.maxIdleConns)

	// Use the time.ParseDuration() function to convert the idle timeout duration string to a
	// time.Duration type.
	duration, err := time.ParseDuration(cfg.db.maxIdleTime)
	if err != nil {
		return nil, err
	}

	// Set the maximum idle timeout.
	db.SetConnMaxIdleTime(duration)

	// Create a context with a 5-second timeout deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use PingContext() to establish a new connection to the database,
	// passing in the context we created above as a parameter.
	// If connection couldn't be established successfully within the 5-second deadline,
	// then this will return an error.
	err = db.PingContext(ctx)
	if err != nil {
		return nil, err
	}

	// Return the sql.DB connection pool.
	return db, nil
}

// To run the application with the flags, you can use the following command:
// go run ./cmd/api/*  -port=4000 -env=development
//     -db-dsn="postgres://greenlight:pa55word@localhost/greenlight?sslmode=disable"
