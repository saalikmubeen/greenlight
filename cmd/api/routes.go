package main

import (
	"expvar"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

// routes is our main application's router.
func (app *application) routes() http.Handler {
	router := httprouter.New()

	// Convert the app.notFoundResponse helper to a http.Handler using the http.HandlerFunc()
	// adapter, and then set it as the custom error handler for 404 Not Found responses.
	router.NotFound = http.HandlerFunc(app.notFoundResponse)

	// Convert app.methodNotAllowedResponse helper to a http.Handler and set it as the custom
	// error handler for 405 Method Not Allowed responses
	router.MethodNotAllowed = http.HandlerFunc(app.methodNotAllowedResponse)

	// healthcheck
	router.HandlerFunc(http.MethodGet, "/v1/healthcheck", app.healthcheckHandler)

	// application metrics handler
	// expvar.Handler() handler displays information about memory usage, along with a
	// reminder of what command-line flags you used when starting the application,
	// all outputted in JSON format.
	router.Handler(http.MethodGet, "/debug/vars", expvar.Handler())

	// Movies handlers. Note, that these movie endpoints use the `requireActivatedUser` middleware.
	// /v1/movies?title=godfather&genres=crime,drama&page=1&page_size=5&sort=-year
	// Required Permission: "movies:read"
	router.HandlerFunc(http.MethodGet, "/v1/movies", app.requirePermissions("movies:read", app.listMoviesHandler))
	// Required Permission: "movies:write"
	router.HandlerFunc(http.MethodPost, "/v1/movies", app.requirePermissions("movies:write", app.createMovieHandler))
	// Required Permission: "movies:read"
	router.HandlerFunc(http.MethodGet, "/v1/movies/:id", app.requirePermissions("movies:read", app.showMovieHandler))
	// Required Permission: "movies:write"
	router.HandlerFunc(http.MethodPatch, "/v1/movies/:id", app.requirePermissions("movies:write", app.updateMovieHandler))
	// Required Permission: "movies:write"
	router.HandlerFunc(http.MethodDelete, "/v1/movies/:id", app.requirePermissions("movies:write", app.deleteMovieHandler))

	// Users handlers
	// Register a new user
	router.HandlerFunc(http.MethodPost, "/v1/users", app.registerUserHandler)
	// Activate the user account who has just registered
	router.HandlerFunc(http.MethodPut, "/v1/users/activated", app.activateUserHandler)

	// Tokens handlers
	// Endpoint to send the activation token or account activation email to the user
	router.HandlerFunc(http.MethodPost, "/v1/tokens/activation", app.createActivationTokenHandler)
	// Log in the user and return an authentication token
	router.HandlerFunc(http.MethodPost, "/v1/tokens/authentication", app.createAuthenticationTokenHandler)

	// Password reset handlers
	// Endpoint where user submits a new password to be stored in the database
	// along with the plain text password reset token they received in their email.
	router.HandlerFunc(http.MethodPut, "/v1/users/password", app.updateUserPasswordHandler)
	// Endpoint where user can request a password reset token or link to be sent to their email
	router.HandlerFunc(http.MethodPost, "/v1/tokens/password-reset", app.createPasswordResetTokenHandler)

	// Use the authenticate() middleware on all requests.
	// Wrap the router with the panic recovery middleware and rate limit middleware.
	/*
		It's important to point out here that the enableCORS() middleware is deliberately
		positioned early in the middleware chain. If we positioned it after our rate limiter,
		for example, any cross-origin requests that exceed the rate limit would not have the Access-Control-Allow-Origin header set. This means in case of client sending too many
		requests that they would be blocked by the client's web browser due to the same-origin
		policy, rather than the client receiving a 429 Too Many Requests response like they should.
	*/
	// The middleware functions are REGISTERED once and run from RIGHT to LEFT upon the
	// application startup in the routes() method. However, for each incoming request, the
	// middleware functions are EXECUTED from LEFT to RIGHT.
	// Registration order:
	// 1. authenticate -> 2. rateLimit -> 3. enableCORS -> 4. recoverPanic -> 5. metrics
	// The order of execution is:
	// 1. metrics -> 2. recoverPanic -> 3. enableCORS -> 4. rateLimit -> 5. authenticate
	// And finally when all the middleware functions have run by calling next.ServeHTTP(w, r)
	// the request is passed to the router for handling, after which the response is passed back
	// through the middleware functions chain in the reverse order i.e any code after
	// next.ServeHTTP(w, r) is executed in the reverse order.
	// So the order of execution for the response is:
	// 1. authenticate -> 2. rateLimit -> 3. enableCORS -> 4. recoverPanic -> 5. metrics
	return app.metrics(app.recoverPanic(app.enableCORS(app.rateLimit(app.authenticate(router)))))

}
