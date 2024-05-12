package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func (app *application) serve() error {
	// Declare an HTTP server using the same settings as in our main() function.

	/* By default  Go’s http.Server writes its own log messages relating to things
	like unrecovered panics, or problems accepting and writing to HTTP connections to
	the standard logger — which means they will be written to the standard error stream
	(instead of standard out like our other log messages),
	and they won’t be in our nice new JSON format.

	Unfortunately, you can’t set http.Server to use our new Logger type directly.
	Instead, you will need to leverage the fact that our Logger satisfies the io.Writer
	interface (thanks to the Write() method that we added to it), and set http.Server
	to use a regular log.Logger instance from the standard library which writes to our
	own Logger as the target destination.
	*/
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", app.config.port),
		Handler: app.routes(),
		// Create a new Go log.Logger instance with the log.New() function, passing in
		// our custom Logger as the first parameter. The "" and 0 indicate that the
		// log.Logger instance should not use a prefix or any flags.
		ErrorLog:     log.New(app.logger, "", 0),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Create a shutdownError channel. We will use this to receive any errors returned
	// by the graceful Shutdown() function.
	shutdownError := make(chan error)

	/*
		* Start a background goroutine.
		To catch the signals, we’ll need to spin up a background goroutine which runs for the
		lifetime of our application. In this background goroutine, we can use the signal.Notify()
		function to listen for specific signals and relay them to a channel for further processing.
	*/
	go func() {
		// Create a quit channel which carries os.Signal values. Use buffered
		// We need to use a buffered channel here because signal.Notify() does not
		// wait for a receiver to be available when sending a signal to the quit channel.
		//  If we had used a regular (non-buffered) channel here instead, a signal could be
		// ‘missed’ if our quit channel is not ready to receive at the exact moment that the
		// signal is sent. By using a buffered channel, we avoid this problem and ensure
		// that we never miss a signal.
		quit := make(chan os.Signal, 1)

		// Use signal.Notify() to listen for incoming SIGINT and SIGTERM signals and relay
		// them to the quit channel. Any other signal will not be caught by signal.Notify()
		// and will retain their default behavior.
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

		// Read the signal from the quit channel. This code will block until a signal is
		// received.
		s := <-quit

		// Log a message to say we caught the signal. Notice that we also call the
		// String() method on the signal to get the signal name and include it in the log
		// entry properties.
		app.logger.PrintInfo("caught signal", map[string]string{
			"signal": s.String(),
		})

		// Create a context with a 5-second timeout.
		// Give any in-flight requests a ‘grace period’ of 5 seconds to complete
		// before the application is terminated.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Call Shutdown() on our server, passing in the context we just made.
		// Shutdown() will return nil if the graceful shutdown was successful, or an
		// error (which may happen because of a problem closing the listeners, or
		// because the shutdown didn't complete before the 5-second context deadline is
		// hit). We relay this return value to the shutdownError channel.
		err := srv.Shutdown(ctx)
		if err != nil {
			shutdownError <- err
		}

		// Log a message to say that we're waiting for any background goroutines to complete
		// their tasks.
		app.logger.PrintInfo("completing background tasks", map[string]string{
			"addr": srv.Addr,
		})

		// Call Wait() to block until our WaitGroup counter is zero. This essentially blocks
		// until the background goroutines have finished. Then we return nil on the shutdownError
		// channel to indicate that the shutdown as compleeted without any issues.
		// Uses sync.WaitGroup to wait for any background goroutines before terminating the application.
		app.wg.Wait()
		shutdownError <- nil

	}()

	// Log a "starting server" message.
	app.logger.PrintInfo("starting server", map[string]string{
		"addr": srv.Addr,
		"env":  app.config.env,
	})

	// Calling Shutdown() on our server will cause ListenAndServer() to immediately
	// return a http.ErrServerClosed error. So, if we see this error, it is actually a good thing
	// and an indication that the graceful shutdown has started. So, we specifically check for this,
	// only returning the error if it is NOT http.ErrServerClosed.
	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Otherwise, we wait to receive the return value from Shutdown() on the shutdownErr
	// channel. If the return value is an error, we know that there was a problem with the
	// graceful shutdown, and we return the error.
	err = <-shutdownError
	if err != nil {
		return err
	}

	// At this point we know that the graceful shutdown completed successfully, and we log
	// a "stopped server" message.
	app.logger.PrintInfo("stopped server", map[string]string{
		"addr": srv.Addr,
	})

	return nil
}

/*
Signal            Description                        Keyboard shortcut          Catchable
SIGINT         Interrupt from keyboard	                Ctrl+C                    Yes
SIGQUIT 	   Quit from keyboard	                    Ctrl+\                    Yes
SIGKILL 	   Kill process (terminate immediately)	      -                       No
SIGTERM 	   Terminate process in orderly manner	      -                       Yes

*/

// To send these signals through command line:
// 1. Find the process id of the running server
// 2. Send the signal to the process id:
// kill -SIGINT <pid>
// kill -SIGTERM <pid>
// kill -SIGQUIT <pid>
// kill -SIGKILL <pid> // This will terminate the process immediately
